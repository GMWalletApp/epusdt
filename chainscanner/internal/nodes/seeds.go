package nodes

import (
	"context"
	"database/sql"
)

// SQL uniqueness uses the original URL spelling. Reuse existing IDs, health,
// credentials and tombstones when a configured or built-in URL has an alias.
type seedRow struct {
	NodeEndpoint
	id      int64
	origin  string
	removed bool
}

type seedRows map[NodeEndpoint][]seedRow

func loadSeedRows(ctx context.Context, tx *sql.Tx) (seedRows, error) {
	rows, err := tx.QueryContext(ctx, `SELECT network,protocol,url,id,origin,removed FROM rpc_nodes
 ORDER BY CASE WHEN removed=0 AND origin IN ('custom','fallback') THEN 0
               WHEN removed<>0 THEN 1 WHEN origin='builtin' THEN 2 ELSE 3 END,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	existing := seedRows{}
	for rows.Next() {
		var r seedRow
		if err = rows.Scan(&r.Network, &r.Protocol, &r.URL, &r.id, &r.origin, &r.removed); err != nil {
			return nil, err
		}
		key := r.identity()
		existing[key] = append(existing[key], r)
	}
	return existing, rows.Err()
}

func (rows seedRows) seed(ctx context.Context, tx *sql.Tx, e NodeEndpoint, origin string) (NodeEndpoint, error) {
	key := e.identity()
	variants := rows[key]
	if len(variants) > 0 {
		e = variants[0].NodeEndpoint
		for _, r := range variants[1:] {
			// Only retire redundant automatically created rows for this seed.
			if !r.removed && (r.origin == "builtin" || isDiscoveryOrigin(r.origin) || r.origin == "config") {
				if _, err := tx.ExecContext(ctx, "DELETE FROM rpc_nodes WHERE id=?", r.id); err != nil {
					return e, err
				}
			}
		}
	}
	_, err := tx.ExecContext(ctx, seedNode, e.Network, e.Protocol, e.URL, origin)
	rows[key] = []seedRow{{NodeEndpoint: e}}
	return e, err
}
