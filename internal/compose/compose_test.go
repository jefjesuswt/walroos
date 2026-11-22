package compose

import (
	"encoding/json"
	"strings"
	"testing"
)

// testValue stands in for a variable's value; it's no secret.
var testValue = strings.ToUpper("value")

const shop = `
name: shop
services:
  web:
    build: ./web
    ports: ["8080:3000"]
    environment:
      API_URL: http://api:4000
      MODE: ${MODE:-production}
    depends_on: [api]
  api:
    build:
      context: ./api
      dockerfile: Dockerfile.prod
    env_file: api/.env
    environment:
      - DATABASE_URL=postgres://shop:${DB_PASSWORD}@db:5432/shop
      - PRICE=$$5
    expose: ["4000"]
    depends_on:
      db: {condition: service_healthy}
  db:
    image: postgres:18
    environment:
      POSTGRES_PASSWORD: ${DB_PASSWORD}
    volumes: [pgdata:/var/lib/postgresql/data]
    restart: unless-stopped
  worker:
    image: me/worker
    command: node worker.js --queue "mail queue"
volumes:
  pgdata:
`

func TestTranslate(t *testing.T) {
	f, err := Parse(shop, map[string]string{"DB_PASSWORD": testValue})
	if err != nil {
		t.Fatal(err)
	}
	src := Source{RepoURL: "https://github.com/me/shop.git", Branch: "main"}
	plan, err := Translate(f, "shop", src, map[string]string{"api/.env": "# api\nLOG_LEVEL=info\nexport TOKEN=\"abc\"\n"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	by := map[string]Planned{}
	for _, p := range plan {
		got = append(got, p.Service)
		by[p.Service] = p
	}
	if strings.Join(got, ",") != "db,api,web,worker" {
		t.Fatalf("order = %v", got)
	}
	db, api, web, worker := by["db"], by["api"], by["web"], by["worker"]
	if db.App != "shop-db" || db.Input.Image != "docker.io/library/postgres:18" || db.Input.Health != "running" || len(db.Volumes) != 1 || db.Volumes[0].Name != "pgdata" ||
		strings.Join(db.Env, ",") != "POSTGRES_PASSWORD="+testValue {
		t.Errorf("db = %+v", db)
	}
	if api.Input.Context != "api" || api.Input.Dockerfile != "Dockerfile.prod" || api.Input.Port != 4000 || api.Input.Health != "tcp" || api.Input.RepoURL != src.RepoURL ||
		strings.Join(api.Env, ",") != "LOG_LEVEL=info,TOKEN=abc,DATABASE_URL=postgres://shop:"+testValue+"@db:5432/shop,PRICE=$5" {
		t.Errorf("api = %+v", api)
	}
	if web.Input.Context != "web" || web.Input.Port != 3000 || strings.Join(web.Env, ",") != "API_URL=http://api:4000,MODE=production" || strings.Join(web.DependsOn, ",") != "api" {
		t.Errorf("web = %+v", web)
	}
	if worker.Input.Image != "docker.io/me/worker" || strings.Join(worker.Input.Command, "|") != "node|worker.js|--queue|mail queue" || worker.Input.Health != "running" {
		t.Errorf("worker = %+v", worker)
	}
	if !strings.Contains(strings.Join(f.Warnings, "\n"), "services.db.restart: ignored") {
		t.Errorf("warnings = %v", f.Warnings)
	}
}

func TestRefused(t *testing.T) {
	svc := func(body string) string { return "services:\n  app:\n" + body }
	cases := map[string]struct{ text, want string }{
		"a variable with no value": {svc("    image: nginx\n    environment: [A=${X}, B=${Y}]\n"), "no value for X, Y"},
		"a required variable":      {svc("    image: nginx\n    environment: [A=${X:?set X}]\n"), "required variables: X: set X"},
		"a bind mount":             {svc("    image: nginx\n    volumes: [./site:/usr/share/nginx/html]\n"), "bind mount isn't supported"},
		"privileged":               {svc("    image: nginx\n    privileged: true\n"), "privileged: not supported"},
		"a value from the shell":   {svc("    image: nginx\n    environment: [TOKEN]\n"), "TOKEN has no value"},
		"a UDP port":               {svc("    image: nginx\n    ports: [\"53:53/udp\"]\n"), "single TCP port"},
		"no image or build":        {svc("    command: x\n"), "needs an image or a build"},
		"build args":               {svc("    build: {context: ., args: {A: b}}\n"), "args aren't supported"},
		"secrets":                  {"secrets: {}\n" + svc("    image: nginx\n"), "secrets: not supported"},
	}
	for name, c := range cases {
		if _, err := Parse(c.text, nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	// Errors only Translate can find.
	translate := map[string]struct{ text, want string }{
		"a pasted build": {"services:\n  app:\n    build: .\n", "needs the compose file from a repo"},
		"a circle":       {"services:\n  a: {image: x, depends_on: [b]}\n  b: {image: x, depends_on: [a]}\n", "in a circle"},
		"an unknown dep": {"services:\n  a: {image: x, depends_on: [nope]}\n", "nope, which isn't a service"},
		"a missing env":  {"services:\n  a: {image: x, env_file: .env}\n", "env_file .env: not in the repo"},
		"a bad name":     {"services:\n  My_App: {image: x}\n", "service names must be"},
	}
	for name, c := range translate {
		f, err := Parse(c.text, nil)
		if err == nil {
			_, err = Translate(f, "shop", Source{}, nil)
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestNormalizeImage(t *testing.T) {
	for in, want := range map[string]string{
		"postgres:18": "docker.io/library/postgres:18", "me/app": "docker.io/me/app", "ghcr.io/me/app:1": "ghcr.io/me/app:1",
		"redis@sha256:abc": "docker.io/library/redis@sha256:abc",
	} {
		if got := normalizeImage(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

// Every list in the plan is a list, never null (the UI reads .length on them).
func TestPlanListsNeverNull(t *testing.T) {
	f, _ := Parse("services:\n  w: {image: busybox}\n", nil)
	plan, err := Translate(f, "p", Source{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(plan)
	if strings.Contains(string(b), "null") {
		t.Fatalf("plan: %s", b)
	}
}
