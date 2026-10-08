package infra

import (
	"net/url"
	"strings"
	"testing"
)

func TestRoleURLsCarryNoAdminCredentials(t *testing.T) {
	a := Admin{URL: "postgres://admin:adminpw@db.example:5432/postgres?sslmode=require&password=adminpw&user=admin&passfile=/root/.pgpass&service=prod&sslcert=/k/c.pem&sslkey=/k/k.pem&connect_timeout=5"}
	for _, host := range []string{"", "/run/seed-db"} {
		got := a.DatabaseURL("app", "organism", "orgpw", host)
		if strings.Contains(got, "adminpw") || strings.Contains(got, "admin@") || strings.Contains(got, "user=admin") ||
			strings.Contains(got, "passfile") || strings.Contains(got, "service") || strings.Contains(got, "/k/") {
			t.Fatalf("host %q: the admin's credentials leaked into another role's URL: %s", host, got)
		}
		u, _ := url.Parse(got)
		if u.User.Username() != "organism" || u.Path != "/app" {
			t.Fatalf("host %q: %s", host, got)
		}
		if host == "" && (u.Query().Get("sslmode") != "require" || u.Query().Get("connect_timeout") != "5") {
			t.Fatalf("harmless settings are kept over TCP: %s", got)
		}
		if host != "" && (u.Query().Get("sslmode") != "disable" || u.Query().Get("host") != host) {
			t.Fatalf("socket URLs are plain: %s", got)
		}
	}
	// The server's location survives (my private PostgreSQL is a socket named in the query).
	local := Admin{URL: "postgres://postgres@/postgres?host=/seed/.seed/run&sslmode=disable"}
	if got := local.DatabaseURL("app", "organism", "pw", ""); !strings.Contains(got, "host=%2Fseed%2F.seed%2Frun") {
		t.Fatalf("the socket location must be kept: %s", got)
	}
	// The kernel's own URLs (no user) keep everything.
	if got := a.DatabaseURL("seed", "", "", ""); !strings.Contains(got, "adminpw") {
		t.Fatalf("my own URL keeps my credentials: %s", got)
	}
}
