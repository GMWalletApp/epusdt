package data

import (
	"context"
	"errors"
	"net/url"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/scanneradmin"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"gorm.io/gorm/clause"
)

type MigrationTarget struct {
	StoreID string `json:"store_id"`
	Target  string `json:"target"`
}
type MigrationIssue struct {
	ID      uint64 `json:"id"`
	Network string `json:"network"`
	Host    string `json:"host"`
	Reason  string `json:"reason"`
}
type NodeMigration struct {
	MigrationTarget
	External  bool             `json:"external"`
	Pending   int              `json:"pending"`
	Completed int              `json:"completed"`
	Conflicts []MigrationIssue `json:"conflicts"`
}

func MigrateScannerNodes(ctx context.Context, c config.ScannerConnection, apply bool, confirm *MigrationTarget) (NodeMigration, error) {
	manifest, err := scanneradmin.Manifest(ctx, c)
	report := NodeMigration{Conflicts: []MigrationIssue{}, External: !c.Embedded}
	if err != nil {
		return report, err
	}
	report.StoreID = manifest.StoreID
	report.Target = "embedded"
	if report.External {
		report.Target, err = c.Endpoint()
		if err != nil {
			return report, err
		}
	}
	if apply && (report.External || confirm != nil) && (confirm == nil || *confirm != report.MigrationTarget) {
		return report, errors.New("confirm the current ChainScanner target")
	}
	legacy, err := ListRpcNodes("")
	if err != nil {
		return report, err
	}
	db := dao.Mdb.WithContext(ctx)
	var completed []uint64
	if err = db.Model(&mdb.ScannerNodeMigration{}).Where("store_id = ?", report.StoreID).Pluck("legacy_id", &completed).Error; err != nil {
		return report, err
	}

	done := map[uint64]bool{}
	for _, id := range completed {
		done[id] = true
	}
	type entry struct {
		options         scanner.NodeOptions
		ids             []uint64
		settings, issue string
	}
	groups := map[scanner.NodeEndpoint]*entry{}
	var ordered []*entry
	for _, row := range legacy {
		endpoint := (scanner.NodeEndpoint{Network: row.Network, Protocol: row.Type, URL: row.Url}).Identity()
		purpose := NormalizeRpcNodePurpose(row.Purpose)
		weight := max(1, row.Weight)
		digest := manifest.Settings("regular", purpose, row.Enabled, weight, row.ApiKey)
		group := groups[endpoint]
		if group == nil {
			enabled := row.Enabled
			group = &entry{settings: digest, options: scanner.NodeOptions{NodeEndpoint: endpoint, Enabled: &enabled, Weight: weight, Purpose: purpose, APIKey: row.ApiKey}}
			groups[endpoint] = group
			ordered = append(ordered, group)
		} else if group.settings != digest {
			group.issue = "legacy_conflict"
		}
		if done[row.ID] {
			report.Completed++
			continue
		}
		group.ids = append(group.ids, row.ID)
	}
	for _, group := range ordered {
		if len(group.ids) == 0 {
			continue
		}
		o := group.options
		matched := false
		if group.issue == "" {
			digest, exists := manifest.Entries[manifest.Key(o.NodeEndpoint)]
			switch {
			case exists && digest == group.settings:
				matched = true
			case exists:
				group.issue = "settings_conflict"
			default:
				if _, err := o.ValidatedEndpoint(); err != nil {
					group.issue = "invalid_endpoint"
				}
			}
		}
		if apply && group.issue == "" && !matched {
			status, err := scanneradmin.Import(ctx, c, report.StoreID, o)
			if status == 400 || status == 409 {
				group.issue = "endpoint_conflict"
			} else if err != nil {
				return report, err
			}
		}
		if group.issue != "" {
			host := ""
			if u, e := url.Parse(o.URL); e == nil {
				host = u.Hostname()
			}
			for _, id := range group.ids {
				report.Conflicts = append(report.Conflicts, MigrationIssue{id, o.Network, host, group.issue})
			}
			continue
		}
		if !apply {
			report.Pending += len(group.ids)
			continue
		}
		receipts := make([]mdb.ScannerNodeMigration, 0, len(group.ids))
		for _, id := range group.ids {
			receipts = append(receipts, mdb.ScannerNodeMigration{StoreID: report.StoreID, LegacyID: id})
		}
		if err = db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(receipts, 100).Error; err != nil {
			return report, err
		}
		report.Completed += len(receipts)
	}
	return report, nil
}
