package nodes

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func (r *NodeRegistry) Handler(consumers map[string]string, token string) (http.Handler, error) {
	consumerHandler, err := engine.ConsumerHandler(r.store, consumers)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return consumerHandler, nil
	}
	if len(token) < 24 {
		return nil, errors.New("admin token must be at least 24 bytes")
	}
	for _, v := range consumers {
		if v == token {
			return nil, errors.New("admin token must be separate from consumer tokens")
		}
	}
	key := sha256.Sum256([]byte(token))
	admin := http.NewServeMux()
	r.registerBlacklistRoutes(admin)
	r.registerManagement(admin)
	admin.HandleFunc("GET /v1/admin/scan-retry-rate", r.scanRetryRate)
	admin.HandleFunc("GET /v1/admin/progress", func(w http.ResponseWriter, req *http.Request) {
		rows, err := r.store.ScanProgress(req.Context())
		nodeReply(w, 200, rows, err)
	})
	admin.HandleFunc("GET /v1/admin/nodes", func(w http.ResponseWriter, req *http.Request) {
		nodes, err := r.List(req.Context(), req.URL.Query().Get("network"))
		nodeReply(w, 200, nodes, err)
	})
	addNode := func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/admin/nodes/import" {
			var storeID string
			if err := r.db.QueryRowContext(req.Context(), "SELECT value FROM registry_identity WHERE id=1").Scan(&storeID); err != nil {
				nodeReply(w, 503, nil, err)
				return
			}
			if req.URL.Query().Get("store_id") != storeID {
				http.Error(w, "scanner database changed", http.StatusPreconditionFailed)
				return
			}
		}
		var e NodeOptions
		d := json.NewDecoder(http.MaxBytesReader(w, req.Body, 8192))
		d.DisallowUnknownFields()
		if d.Decode(&e) != nil || d.Decode(new(any)) != io.EOF {
			nodeReply(w, 400, nil, errInvalidNode)
			return
		}
		n, err := r.insert(req.Context(), e, req.URL.Path == "/v1/admin/nodes/fallback", req.URL.Path == "/v1/admin/nodes/import")
		nodeReply(w, 201, n, err)
	}
	admin.HandleFunc("POST /v1/admin/nodes", addNode)
	admin.HandleFunc("POST /v1/admin/nodes/import", addNode)
	admin.HandleFunc("POST /v1/admin/nodes/fallback", addNode)
	admin.HandleFunc("DELETE /v1/admin/nodes/{id}", func(w http.ResponseWriter, req *http.Request) {
		id, err := nodeID(req)
		if err == nil {
			err = r.Delete(req.Context(), id)
		}
		nodeReply(w, 204, nil, err)
	})
	admin.HandleFunc("POST /v1/admin/nodes/{id}/test", func(w http.ResponseWriter, req *http.Request) {
		id, err := nodeID(req)
		var n Node
		if err == nil {
			n, err = r.Test(req.Context(), id)
		}
		nodeReply(w, 200, n, err)
	})
	admin.HandleFunc("POST /v1/admin/nodes/test", func(w http.ResponseWriter, req *http.Request) {
		if !r.batch.TryLock() {
			http.Error(w, "batch test already running", 409)
			return
		}
		defer r.batch.Unlock()
		// A full inventory can take several per-node timeouts. Client cancellation stops the batch.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		nodes, err := r.testAll(req.Context(), req.URL.Query().Get("network"), req.URL.Query().Get("include_manual") == "true")
		nodeReply(w, 200, nodes, err)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasPrefix(req.URL.Path, "/v1/admin/") {
			consumerHandler.ServeHTTP(w, req)
			return
		}
		v, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
		candidate := sha256.Sum256([]byte(v))
		if !ok || subtle.ConstantTimeCompare(candidate[:], key[:]) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		admin.ServeHTTP(w, req)
	}), nil
}

func nodeID(req *http.Request) (int64, error) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errInvalidNode
	}
	return id, nil
}
func nodeReply(w http.ResponseWriter, code int, value any, err error) {
	if err != nil {
		switch {
		case errors.Is(err, errBlacklistHost):
			http.Error(w, err.Error(), 400)
		case errors.Is(err, errDisabledNode), errors.Is(err, errConfiguredRule):
			http.Error(w, err.Error(), 409)
		case errors.Is(err, errInvalidNode):
			http.Error(w, "invalid node", 400)
		case errors.Is(err, errDuplicateNode):
			http.Error(w, "node already exists", 409)
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "unknown node", 404)
		default:
			http.Error(w, "node store unavailable", 503)
		}
		return
	}
	if code == 204 {
		w.WriteHeader(code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(value)
}

func (r *NodeRegistry) testAll(ctx context.Context, network string, includeManual bool) ([]Node, error) {
	nodes, err := r.List(ctx, network)
	if err != nil {
		return nil, err
	}
	jobs := make(chan int64)
	var wg sync.WaitGroup
	var once sync.Once
	for range min(len(nodes), cap(r.slots)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				_, e := r.Test(ctx, id)
				if e != nil && !errors.Is(e, sql.ErrNoRows) && !errors.Is(e, errDisabledNode) {
					once.Do(func() { err = e })
				}
			}
		}()
	}
send:
	for _, n := range nodes {
		if !n.Enabled || (!includeManual && n.Purpose == "manual_verify") {
			continue
		}
		select {
		case <-ctx.Done():
			break send
		case jobs <- n.ID:
		}
	}
	close(jobs)
	wg.Wait()
	if err != nil {
		return nil, err
	}
	return r.List(ctx, network)
}
