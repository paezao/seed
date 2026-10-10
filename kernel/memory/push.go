package memory

import (
	"context"
	"time"

	"seed/kernel/ids"
)

// PushSubscription is a browser my owner turned notifications on in.
type PushSubscription struct {
	ID         string     `json:"id"`
	Endpoint   string     `json:"-"`
	P256dh     string     `json:"-"`
	Auth       string     `json:"-"`
	UserAgent  string     `json:"user_agent"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
}

// SavePushSubscription adds a subscription (or refreshes one for the same endpoint).
func (s *Store) SavePushSubscription(ctx context.Context, p *PushSubscription) error {
	if p.ID == "" {
		p.ID = ids.New("push")
	}
	return s.Pool.QueryRow(ctx, `INSERT INTO push_subscriptions (id, endpoint, p256dh, auth, user_agent) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (endpoint) DO UPDATE SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, user_agent = EXCLUDED.user_agent, last_error = ''
		RETURNING id, created_at`, p.ID, p.Endpoint, p.P256dh, p.Auth, p.UserAgent).Scan(&p.ID, &p.CreatedAt)
}

func (s *Store) PushSubscriptions(ctx context.Context) ([]*PushSubscription, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, endpoint, p256dh, auth, user_agent, created_at, last_sent_at, last_error FROM push_subscriptions ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PushSubscription
	for rows.Next() {
		var p PushSubscription
		if err := rows.Scan(&p.ID, &p.Endpoint, &p.P256dh, &p.Auth, &p.UserAgent, &p.CreatedAt, &p.LastSentAt, &p.LastError); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *Store) DeletePushSubscription(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE id=$1`, id)
	return err
}

// MarkPush records how the last delivery to a subscription went.
func (s *Store) MarkPush(ctx context.Context, id, errMsg string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE push_subscriptions SET last_sent_at = CASE WHEN $2 = '' THEN now() ELSE last_sent_at END, last_error = $2 WHERE id=$1`, id, errMsg)
	return err
}
