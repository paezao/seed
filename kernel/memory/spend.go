package memory

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Spend is one model call's cost.
type Spend struct {
	Kind             string // chat, evolution, routine, health, other
	Ref              string
	Model            string
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	CostUSD          float64
	Priced           bool // the provider said what it cost
}

func (s *Store) AddSpend(ctx context.Context, sp Spend) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO spend (kind, ref, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, priced)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, sp.Kind, sp.Ref, sp.Model, sp.InputTokens, sp.OutputTokens,
		sp.CacheReadTokens, sp.CacheWriteTokens, sp.CostUSD, sp.Priced)
	return err
}

// SpentSince is what my model calls cost since t.
func (s *Store) SpentSince(ctx context.Context, t time.Time) (float64, error) {
	var v float64
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(sum(cost_usd), 0) FROM spend WHERE at >= $1`, t).Scan(&v)
	return v, err
}

type SpendTotal struct {
	Key   string  `json:"key"`
	Ref   string  `json:"ref,omitempty"`
	Label string  `json:"label,omitempty"` // what Ref is, for people
	Cost  float64 `json:"cost_usd"`
	Calls int     `json:"calls"`
}

type SpendReport struct {
	From     time.Time    `json:"from"`
	Total    float64      `json:"total_usd"`
	Calls    int          `json:"calls"`
	Unpriced int          `json:"unpriced_calls"`
	ByKind   []SpendTotal `json:"by_kind"`
	ByDay    []SpendTotal `json:"by_day"` // key: YYYY-MM-DD (UTC)
	Top      []SpendTotal `json:"top"`    // key: kind, ref: what
}

// SpendReportSince sums spending since from.
func (s *Store) SpendReportSince(ctx context.Context, from time.Time) (*SpendReport, error) {
	r := &SpendReport{From: from, ByKind: []SpendTotal{}, ByDay: []SpendTotal{}, Top: []SpendTotal{}}
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(sum(cost_usd), 0), count(*), count(*) FILTER (WHERE NOT priced) FROM spend WHERE at >= $1`, from).
		Scan(&r.Total, &r.Calls, &r.Unpriced)
	if err != nil {
		return nil, err
	}
	collect := func(q string, withRef bool) ([]SpendTotal, error) {
		rows, err := s.Pool.Query(ctx, q, from)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(row pgx.CollectableRow) (SpendTotal, error) {
			var t SpendTotal
			if withRef {
				return t, row.Scan(&t.Key, &t.Ref, &t.Cost, &t.Calls)
			}
			return t, row.Scan(&t.Key, &t.Cost, &t.Calls)
		})
	}
	if r.ByKind, err = collect(`SELECT kind, sum(cost_usd), count(*) FROM spend WHERE at >= $1 GROUP BY kind ORDER BY 2 DESC`, false); err != nil {
		return nil, err
	}
	if r.ByDay, err = collect(`SELECT to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), sum(cost_usd), count(*) FROM spend WHERE at >= $1 GROUP BY 1 ORDER BY 1`, false); err != nil {
		return nil, err
	}
	if r.Top, err = collect(`SELECT kind, ref, sum(cost_usd), count(*) FROM spend WHERE at >= $1 AND ref <> '' GROUP BY kind, ref ORDER BY 3 DESC LIMIT 10`, true); err != nil {
		return nil, err
	}
	return r, nil
}
