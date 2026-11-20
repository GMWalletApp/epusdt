package data

import (
	"fmt"
	"math/rand"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/dromara/carbon/v2"
)

const (
	RpcFailoverThreshold = 3
	RpcFailoverCooldown  = 60 * time.Second
)

var gRpcFailover = struct {
	sync.Mutex
	failures      map[uint64]int
	cooldownUntil map[uint64]time.Time
}{
	failures:      make(map[uint64]int),
	cooldownUntil: make(map[uint64]time.Time),
}

func NormalizeRpcNodePurpose(purpose string) string {
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case mdb.RpcNodePurposeManualVerify:
		return mdb.RpcNodePurposeManualVerify
	case mdb.RpcNodePurposeBoth:
		return mdb.RpcNodePurposeBoth
	default:
		return mdb.RpcNodePurposeGeneral
	}
}

// ListRpcNodes returns rows optionally filtered by network.
func ListRpcNodes(network string) ([]mdb.RpcNode, error) {
	var rows []mdb.RpcNode
	tx := dao.Mdb.Model(&mdb.RpcNode{})
	if network != "" {
		tx = tx.Where("network = ?", strings.ToLower(network))
	}
	err := tx.Order("id ASC").Find(&rows).Error
	return rows, err
}

// ListRpcNodesForHealth returns nodes that should be probed by the periodic
// health job. Manual verification nodes are intentionally excluded because
// they may be paid/high-limit endpoints that should only be used on demand.
func ListRpcNodesForHealth() ([]mdb.RpcNode, error) {
	var rows []mdb.RpcNode
	err := dao.Mdb.Model(&mdb.RpcNode{}).
		Where("(purpose IN ? OR purpose = '' OR purpose IS NULL)", []string{mdb.RpcNodePurposeGeneral, mdb.RpcNodePurposeBoth}).
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

// ListGeneralRpcCandidates returns enabled non-manual nodes for ordinary
// scanners/listeners. Down nodes are skipped; ok nodes are ordered before
// unknown/bootstrap nodes.
func ListGeneralRpcCandidates(network, nodeType string) ([]mdb.RpcNode, error) {
	var rows []mdb.RpcNode
	err := dao.Mdb.Model(&mdb.RpcNode{}).
		Where("network = ?", strings.ToLower(strings.TrimSpace(network))).
		Where("type = ?", strings.ToLower(strings.TrimSpace(nodeType))).
		Where("enabled = ?", true).
		Where("(purpose IN ? OR purpose = '' OR purpose IS NULL)", []string{mdb.RpcNodePurposeGeneral, mdb.RpcNodePurposeBoth}).
		Where("(status IN ? OR status = '' OR status IS NULL)", []string{mdb.RpcNodeStatusOk, mdb.RpcNodeStatusUnknown}).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	buckets := make([][]mdb.RpcNode, 2)
	for _, row := range rows {
		row.Purpose = NormalizeRpcNodePurpose(row.Purpose)
		if row.Status == mdb.RpcNodeStatusOk {
			buckets[0] = append(buckets[0], row)
		} else {
			buckets[1] = append(buckets[1], row)
		}
	}

	out := make([]mdb.RpcNode, 0, len(rows))
	for _, bucket := range buckets {
		sortRpcNodes(bucket)
		out = append(out, bucket...)
	}
	return out, nil
}

func SelectGeneralRpcNode(network, nodeType string, excludeIDs ...uint64) (*mdb.RpcNode, error) {
	rows, err := ListGeneralRpcCandidates(network, nodeType)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	excluded := make(map[uint64]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		if id > 0 {
			excluded[id] = struct{}{}
		}
	}
	for i := range rows {
		if _, ok := excluded[rows[i].ID]; ok {
			continue
		}
		if IsRpcNodeCoolingDown(rows[i].ID) {
			continue
		}
		return &rows[i], nil
	}
	if len(excluded) == 0 {
		for i := range rows {
			return &rows[i], nil
		}
	}
	return nil, nil
}

// GetRpcNodeByID fetches one row.
func GetRpcNodeByID(id uint64) (*mdb.RpcNode, error) {
	row := new(mdb.RpcNode)
	err := dao.Mdb.Model(row).Limit(1).Find(row, id).Error
	return row, err
}

// CreateRpcNode inserts a row.
func CreateRpcNode(row *mdb.RpcNode) error {
	row.Purpose = NormalizeRpcNodePurpose(row.Purpose)
	return dao.Mdb.Create(row).Error
}

// UpdateRpcNodeFields patches mutable columns.
func UpdateRpcNodeFields(id uint64, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	if purpose, ok := fields["purpose"].(string); ok {
		fields["purpose"] = NormalizeRpcNodePurpose(purpose)
	}
	return dao.Mdb.Model(&mdb.RpcNode{}).Where("id = ?", id).Updates(fields).Error
}

// DeleteRpcNodeByID soft-deletes the row.
func DeleteRpcNodeByID(id uint64) error {
	return dao.Mdb.Where("id = ?", id).Delete(&mdb.RpcNode{}).Error
}

// SelectRpcNode picks a healthy RPC endpoint for a (network, type) pair.
// Strategy: weighted random among rows where enabled=true AND status=ok.
// Falls back to enabled rows with status=unknown when no health check has
// run yet. Explicitly down rows are not selected. Manual verification nodes
// are excluded so paid/manual-only RPCs are never used by scanners/listeners.
func SelectRpcNode(network, nodeType string) (*mdb.RpcNode, error) {
	var rows []mdb.RpcNode
	err := dao.Mdb.Model(&mdb.RpcNode{}).
		Where("network = ?", strings.ToLower(network)).
		Where("type = ?", strings.ToLower(nodeType)).
		Where("enabled = ?", true).
		Where("(purpose IN ? OR purpose = '' OR purpose IS NULL)", []string{mdb.RpcNodePurposeGeneral, mdb.RpcNodePurposeBoth}).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	healthy := make([]mdb.RpcNode, 0, len(rows))
	bootstrap := make([]mdb.RpcNode, 0, len(rows))
	for _, r := range rows {
		switch r.Status {
		case mdb.RpcNodeStatusOk:
			healthy = append(healthy, r)
		case "", mdb.RpcNodeStatusUnknown:
			bootstrap = append(bootstrap, r)
		}
	}
	candidates := healthy
	if len(candidates) == 0 {
		candidates = bootstrap
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	return pickWeighted(candidates), nil
}

// ListManualPaymentRpcCandidates returns enabled RPC nodes for manual payment
// verification. Ordinary RPCs are tried first to avoid paid endpoint usage;
// manual_verify nodes are used only as a fallback.
func ListManualPaymentRpcCandidates(network, nodeType string) ([]mdb.RpcNode, error) {
	var rows []mdb.RpcNode
	err := dao.Mdb.Model(&mdb.RpcNode{}).
		Where("network = ?", strings.ToLower(strings.TrimSpace(network))).
		Where("type = ?", strings.ToLower(strings.TrimSpace(nodeType))).
		Where("enabled = ?", true).
		Where("(status IN ? OR status = '' OR status IS NULL)", []string{mdb.RpcNodeStatusOk, mdb.RpcNodeStatusUnknown}).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	buckets := make([][]mdb.RpcNode, 4)
	for _, row := range rows {
		row.Purpose = NormalizeRpcNodePurpose(row.Purpose)
		switch row.Purpose {
		case mdb.RpcNodePurposeGeneral, mdb.RpcNodePurposeBoth:
			if row.Status == mdb.RpcNodeStatusOk {
				buckets[0] = append(buckets[0], row)
			} else {
				buckets[1] = append(buckets[1], row)
			}
		case mdb.RpcNodePurposeManualVerify:
			if row.Status == mdb.RpcNodeStatusOk {
				buckets[2] = append(buckets[2], row)
			} else {
				buckets[3] = append(buckets[3], row)
			}
		}
	}

	out := make([]mdb.RpcNode, 0, len(rows))
	for _, bucket := range buckets {
		sortRpcNodes(bucket)
		out = append(out, bucket...)
	}
	return out, nil
}

func sortRpcNodes(rows []mdb.RpcNode) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Weight != rows[j].Weight {
			return rows[i].Weight > rows[j].Weight
		}
		return rows[i].ID < rows[j].ID
	})
}

func RecordRpcNodeSuccess(nodeID uint64) {
	if nodeID == 0 {
		return
	}
	gRpcFailover.Lock()
	defer gRpcFailover.Unlock()
	delete(gRpcFailover.failures, nodeID)
	delete(gRpcFailover.cooldownUntil, nodeID)
}

func RecordRpcNodeFailure(nodeID uint64) (int, bool) {
	if nodeID == 0 {
		return 0, false
	}
	now := time.Now()
	gRpcFailover.Lock()
	defer gRpcFailover.Unlock()
	if until, ok := gRpcFailover.cooldownUntil[nodeID]; ok {
		if now.Before(until) {
			return gRpcFailover.failures[nodeID], true
		}
		delete(gRpcFailover.cooldownUntil, nodeID)
		delete(gRpcFailover.failures, nodeID)
	}

	gRpcFailover.failures[nodeID]++
	failures := gRpcFailover.failures[nodeID]
	if failures >= RpcFailoverThreshold {
		gRpcFailover.failures[nodeID] = 0
		gRpcFailover.cooldownUntil[nodeID] = now.Add(RpcFailoverCooldown)
		return failures, true
	}
	return failures, false
}

func IsRpcNodeCoolingDown(nodeID uint64) bool {
	if nodeID == 0 {
		return false
	}
	now := time.Now()
	gRpcFailover.Lock()
	defer gRpcFailover.Unlock()
	until, ok := gRpcFailover.cooldownUntil[nodeID]
	if !ok {
		return false
	}
	if now.Before(until) {
		return true
	}
	delete(gRpcFailover.cooldownUntil, nodeID)
	delete(gRpcFailover.failures, nodeID)
	return false
}

func RpcNodeLogLabel(node mdb.RpcNode) string {
	return fmt.Sprintf("id=%d network=%s type=%s endpoint=%s", node.ID, node.Network, node.Type, safeRpcEndpoint(node.Url))
}

func safeRpcEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		if parsed.Scheme != "" {
			return parsed.Scheme + "://" + parsed.Host
		}
		return parsed.Host
	}
	cut := strings.IndexAny(raw, "/?")
	if cut >= 0 {
		return raw[:cut]
	}
	return raw
}

func ResetRpcFailoverForTest() {
	gRpcFailover.Lock()
	defer gRpcFailover.Unlock()
	gRpcFailover.failures = make(map[uint64]int)
	gRpcFailover.cooldownUntil = make(map[uint64]time.Time)
}

// pickWeighted chooses one row using the Weight column. Weights < 1 are
// coerced to 1 so an admin-zeroed row still has a chance (use Enabled=false
// to truly disable).
func pickWeighted(rows []mdb.RpcNode) *mdb.RpcNode {
	total := 0
	for i := range rows {
		w := rows[i].Weight
		if w < 1 {
			w = 1
		}
		total += w
	}
	if total <= 0 {
		return &rows[0]
	}
	pick := rand.Intn(total)
	acc := 0
	for i := range rows {
		w := rows[i].Weight
		if w < 1 {
			w = 1
		}
		acc += w
		if pick < acc {
			return &rows[i]
		}
	}
	return &rows[len(rows)-1]
}

// UpdateRpcNodeHealth stamps status/latency + last_checked_at.
func UpdateRpcNodeHealth(id uint64, status string, latencyMs int) error {
	return dao.Mdb.Model(&mdb.RpcNode{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":          status,
			"last_latency_ms": latencyMs,
			"last_checked_at": carbon.Now().StdTime(),
		}).Error
}
