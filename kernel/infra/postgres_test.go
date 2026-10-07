package infra

import "testing"

func TestDatabaseURL(t *testing.T) {
	a := Admin{URL: "postgres://seed:pw@/postgres?host=%2Fseed%2F.seed%2Frun&sslmode=disable"}
	if got := a.DatabaseURL("tasks_app", "org", "s3", ""); got != "postgres://org:s3@/tasks_app?host=%2Fseed%2F.seed%2Frun&sslmode=disable" {
		t.Errorf("socket url: %s", got)
	}
	if got := a.DatabaseURL("tasks_evo", "evo", "x", "/run/seed-db"); got != "postgres://evo:x@/tasks_evo?host=%2Frun%2Fseed-db&sslmode=disable" {
		t.Errorf("sandbox socket url: %s", got)
	}
	tcp := Admin{URL: "postgres://seed:pw@127.0.0.1:5432/postgres?sslmode=disable"}
	if got := tcp.DatabaseURL("db", "", "", "db.internal:5432"); got != "postgres://seed:pw@db.internal:5432/db?sslmode=disable" {
		t.Errorf("tcp url: %s", got)
	}
}
