package app

import (
	"cmp"
	"context"
	"crypto/rand"
	"strings"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var ErrNoTemplate = whttp.NotFound("template not found: pick one of GET /api/templates")

// Template is a common service ready to run: a published image, the port it answers on, where
// it keeps its data and the variables it needs. Creating an app from one fills all that in;
// after that it's an ordinary app (deploys, env, domains, volumes).
type Template struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Category    string         `json:"category"`
	Description string         `json:"description"`
	Logo        string         `json:"logo,omitempty"` // a Simple Icons slug the UI draws
	Image       string         `json:"image"`
	Port        int            `json:"port"`
	HealthPath  string         `json:"health_path"`
	Volumes     []agent.Volume `json:"volumes,omitempty"`
	Env         []TemplateEnv  `json:"env,omitempty"`
}

// TemplateEnv is a variable a template sets. Generate fills Value with a random password.
type TemplateEnv struct {
	Key      string `json:"key"`
	Value    string `json:"value,omitempty"`
	Generate bool   `json:"generate,omitempty"`
}

func vol(name, target string) agent.Volume { return agent.Volume{Name: name, Target: target} }

// Templates are checked on real podman (TestTemplatesPodman): each image starts with its
// variables and answers its health path. One container each; ones that need a command line
// (MinIO) or several containers (Plausible) wait for compose.
var Templates = []Template{
	{ID: "nginx", Name: "nginx", Category: "Web server", Logo: "nginx", Description: "Static files or a reverse proxy.",
		Image: "docker.io/library/nginx:stable-alpine", Port: 80, HealthPath: "/", Volumes: []agent.Volume{vol("html", "/usr/share/nginx/html")}},
	{ID: "caddy", Name: "Caddy", Category: "Web server", Logo: "caddy", Description: "Web server and reverse proxy with a short config.",
		Image: "docker.io/library/caddy:2-alpine", Port: 80, HealthPath: "/", Volumes: []agent.Volume{vol("data", "/data"), vol("config", "/config")}},
	{ID: "adminer", Name: "Adminer", Category: "Database tools", Description: "Database admin in one page: PostgreSQL, MySQL, MariaDB.",
		Image: "docker.io/library/adminer:latest", Port: 8080, HealthPath: "/"},
	{ID: "pgadmin", Name: "pgAdmin", Category: "Database tools", Logo: "postgresql", Description: "The PostgreSQL admin UI.",
		Image: "docker.io/dpage/pgadmin4:latest", Port: 80, HealthPath: "/misc/ping", Volumes: []agent.Volume{vol("data", "/var/lib/pgadmin")},
		Env: []TemplateEnv{{Key: "PGADMIN_DEFAULT_EMAIL", Value: "admin@example.com"}, {Key: "PGADMIN_DEFAULT_PASSWORD", Generate: true}}},
	{ID: "n8n", Name: "n8n", Category: "Automation", Logo: "n8n", Description: "Workflow automation with a visual editor.",
		Image: "docker.io/n8nio/n8n:latest", Port: 5678, HealthPath: "/healthz", Volumes: []agent.Volume{vol("data", "/home/node/.n8n")}},
	{ID: "uptime-kuma", Name: "Uptime Kuma", Category: "Monitoring", Logo: "uptimekuma", Description: "Uptime checks and status pages.",
		Image: "docker.io/louislam/uptime-kuma:1", Port: 3001, HealthPath: "/", Volumes: []agent.Volume{vol("data", "/app/data")}},
	{ID: "grafana", Name: "Grafana", Category: "Monitoring", Logo: "grafana", Description: "Dashboards for metrics and logs.",
		Image: "docker.io/grafana/grafana:latest", Port: 3000, HealthPath: "/api/health", Volumes: []agent.Volume{vol("data", "/var/lib/grafana")},
		Env: []TemplateEnv{{Key: "GF_SECURITY_ADMIN_PASSWORD", Generate: true}}},
	{ID: "prometheus", Name: "Prometheus", Category: "Monitoring", Logo: "prometheus", Description: "Metrics collection and queries.",
		Image: "docker.io/prom/prometheus:latest", Port: 9090, HealthPath: "/-/healthy", Volumes: []agent.Volume{vol("data", "/prometheus")}},
	{ID: "gitea", Name: "Gitea", Category: "Git", Logo: "gitea", Description: "Self-hosted Git with issues and pull requests.",
		Image: "docker.io/gitea/gitea:latest", Port: 3000, HealthPath: "/api/healthz", Volumes: []agent.Volume{vol("data", "/data")}},
	{ID: "mailpit", Name: "Mailpit", Category: "Development", Description: "Catches the mail your apps send (SMTP on 1025) and shows it.",
		Image: "docker.io/axllent/mailpit:latest", Port: 8025, HealthPath: "/"},
	{ID: "meilisearch", Name: "Meilisearch", Category: "Search", Logo: "meilisearch", Description: "Fast full-text search API.",
		Image: "docker.io/getmeili/meilisearch:latest", Port: 7700, HealthPath: "/health", Volumes: []agent.Volume{vol("data", "/meili_data")},
		Env: []TemplateEnv{{Key: "MEILI_MASTER_KEY", Generate: true}}},
	{ID: "rabbitmq", Name: "RabbitMQ", Category: "Messaging", Logo: "rabbitmq", Description: "Message broker (AMQP on 5672) with its management UI.",
		Image: "docker.io/library/rabbitmq:4-management", Port: 15672, HealthPath: "/", Volumes: []agent.Volume{vol("data", "/var/lib/rabbitmq")},
		Env: []TemplateEnv{{Key: "RABBITMQ_DEFAULT_USER", Value: "admin"}, {Key: "RABBITMQ_DEFAULT_PASS", Generate: true}}},
	{ID: "vaultwarden", Name: "Vaultwarden", Category: "Security", Logo: "vaultwarden", Description: "Bitwarden-compatible password manager server.",
		Image: "docker.io/vaultwarden/server:latest", Port: 80, HealthPath: "/alive", Volumes: []agent.Volume{vol("data", "/data")}},
	{ID: "excalidraw", Name: "Excalidraw", Category: "Productivity", Logo: "excalidraw", Description: "Whiteboard for sketching diagrams.",
		Image: "docker.io/excalidraw/excalidraw:latest", Port: 80, HealthPath: "/"},
}

// FromTemplateInput is what the UI sends to create an app from a template.
type FromTemplateInput struct {
	Template    string `json:"template"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Project     string `json:"project"`
	Node        string `json:"node"`
}

// CreateFromTemplate creates the app with the template's image, port, volumes and variables
// (passwords generated here, stored sealed like any env) and queues its first deploy.
func (s *Service) CreateFromTemplate(ctx context.Context, in FromTemplateInput) (App, error) {
	var t *Template
	for i := range Templates {
		if Templates[i].ID == in.Template {
			t = &Templates[i]
		}
	}
	if t == nil {
		return App{}, ErrNoTemplate
	}
	a, err := s.Create(ctx, CreateInput{Name: in.Name, Description: cmp.Or(in.Description, t.Description), Project: in.Project, Node: in.Node,
		Image: t.Image, Port: t.Port, HealthPath: t.HealthPath})
	if err != nil {
		return App{}, err
	}
	if len(t.Volumes) > 0 {
		if a, err = s.SetVolumes(ctx, a.Name, t.Volumes); err != nil {
			return App{}, err
		}
	}
	if len(t.Env) > 0 {
		var vars []workspace.EnvVar
		for _, e := range t.Env {
			v := workspace.EnvVar{Key: e.Key, Value: e.Value}
			if e.Generate {
				v.Value, v.Secret = strings.ToLower(rand.Text()), true
			}
			vars = append(vars, v)
		}
		if err := s.SetEnv(ctx, a.Name, vars); err != nil {
			return App{}, err
		}
	}
	if _, err := s.Deploy(ctx, a.Name, ""); err != nil {
		return a, err
	}
	return s.Get(ctx, a.Name)
}
