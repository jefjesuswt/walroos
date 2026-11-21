package database

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/project/lookup"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/names"
)

const backupTimeout = time.Hour

// Service keeps the databases, runs their scheduled backups and links them to apps.
type Service struct {
	store  *Store
	nodes  *fleet.Service
	agents *agent.Client
	box    *secretbox.Box
	apps   *app.Service
	now    func() time.Time
}

// placement: databases run on prod nodes, like apps (they share the project network).
var placement = fleet.Placement{Role: "prod", NoNode: ErrNoNode, WrongRole: ErrNodeRole}

func NewService(store *Store, nodes *fleet.Service, agents *agent.Client, box *secretbox.Box, apps *app.Service) *Service {
	return &Service{store: store, nodes: nodes, agents: agents, box: box, apps: apps, now: time.Now}
}

var ()

// Engines lists what can be created, from the agent's table.
func Engines() []agent.Engine { return agent.Engines }

// Create stores the database and asks the node to start it. The password is generated here and
// never shown in lists; the connection URL carries it.
func (s *Service) Create(ctx context.Context, in CreateInput) (Database, error) {
	e, ok := engineOf(in.Engine)
	if !names.DNSLabel(in.Name) || in.Name == "engines" || !ok || !slices.Contains(e.Versions, in.Version) { // "engines" is a route
		return Database{}, ErrInvalid
	}
	if _, err := s.store.Get(ctx, in.Name); err == nil {
		return Database{}, ErrExists
	}
	cred, err := credentials(in)
	if err != nil {
		return Database{}, err
	}
	d, err := s.place(ctx, in, e)
	if err != nil {
		return Database{}, err
	}
	d.User, d.DB = cred.user, cred.db
	if d.password, err = s.box.Seal(cred.password); err != nil {
		return Database{}, err
	}
	return s.start(ctx, d, cred.password)
}

type credential struct{ user, db, password string }

// credentials are the ones asked for, else a user and database named after it and a random
// 32-character password (letters and digits: no quoting in an env file or a URL).
func credentials(in CreateInput) (credential, error) {
	ident := strings.ReplaceAll(in.Name, "-", "_")
	if ident[0] >= '0' && ident[0] <= '9' {
		ident = "db_" + ident // SQL identifiers can't start with a digit
	}
	c := credential{
		user:     cmp.Or(in.User, ident),
		db:       cmp.Or(in.DB, ident),
		password: cmp.Or(in.Password, rand.Text()+rand.Text()[:6]),
	}
	if !names.SQLIdent(c.user) || !names.SQLIdent(c.db) || !agent.ValidPassword(c.password) || len(strings.TrimSpace(in.Description)) > 500 {
		return credential{}, ErrInvalid
	}
	return c, nil
}

// place resolves the project and the node: the row as it will be stored, without credentials.
func (s *Service) place(ctx context.Context, in CreateInput, e agent.Engine) (Database, error) {
	projectID, err := lookup.ID(ctx, s.store.db, in.Project)
	if err != nil {
		return Database{}, err
	}
	node, err := s.nodes.Pick(ctx, placement, in.Node, nil)
	if err != nil {
		return Database{}, err
	}
	return Database{ID: randomID(), Name: in.Name, NodeID: node.ID, NodeName: node.Name, ProjectID: projectID,
		Project: cmp.Or(in.Project, lookup.Default), Engine: e.ID, Version: in.Version, Description: strings.TrimSpace(in.Description),
		BackupEveryHours: 24, BackupKeep: 7, CreatedAt: s.now().UTC()}, nil
}

// start stores the row, then has the node run the database. Row first: if the node fails, the
// row goes; the other way round a failed insert would leave a database running that the hub
// doesn't know.
func (s *Service) start(ctx context.Context, d Database, password string) (Database, error) {
	address, tokenHash, err := s.nodes.AgentTarget(ctx, d.NodeID)
	if err != nil {
		return Database{}, err
	}
	if err := s.store.Insert(ctx, d); err != nil {
		return Database{}, err
	}
	live, err := s.agents.CreateDatabase(ctx, address, tokenHash, agent.DatabaseSpec{
		Name: d.Name, Engine: d.Engine, Version: d.Version, User: d.User, DB: d.DB, Password: password, Network: agent.ProjectNetwork(d.Project)})
	if err != nil {
		s.store.Delete(ctx, d.ID)
		return Database{}, agentErr(err)
	}
	d.State = live.State
	return s.withConn(d, false), nil
}

func engineOf(id string) (agent.Engine, bool) {
	for _, e := range agent.Engines {
		if e.ID == id {
			return e, true
		}
	}
	return agent.Engine{}, false
}

// withConn fills Host and Port, and the URL (with the password) when url is set.
func (s *Service) withConn(d Database, url bool) Database {
	e, _ := engineOf(d.Engine)
	d.Host, d.Port = agent.DatabaseContainer(d.Name), e.Port
	if !url {
		return d
	}
	pw, err := s.box.Open(d.password)
	if err != nil {
		return d // wrong key: no URL rather than a broken one
	}
	switch d.Engine {
	case "redis", "valkey":
		d.URL = fmt.Sprintf("redis://:%s@%s:%d/0", pw, d.Host, d.Port)
	case "mongo": // the user is root on admin, where it authenticates
		d.URL = fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=admin", d.User, pw, d.Host, d.Port, d.DB)
	default:
		d.URL = fmt.Sprintf("%s://%s:%s@%s:%d/%s", e.Scheme, d.User, pw, d.Host, d.Port, d.DB)
	}
	return d
}

// List returns every database with its state on the node, asking each node once.
func (s *Service) List(ctx context.Context) ([]Database, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	live := s.states(ctx)
	for i := range list {
		list[i] = s.withState(ctx, s.withConn(list[i], false), live)
	}
	return list, nil
}

func (s *Service) Get(ctx context.Context, name string) (Database, error) {
	d, err := s.store.Get(ctx, name)
	if err != nil {
		return d, err
	}
	return s.withState(ctx, s.withConn(d, true), s.states(ctx)), nil
}

// states seeds the per-node cache with nil for nodes whose heartbeats stopped.
func (s *Service) states(ctx context.Context) map[string][]agent.Database {
	live := map[string][]agent.Database{}
	if online, err := s.nodes.Online(ctx); err == nil {
		for id, up := range online {
			if !up {
				live[id] = nil
			}
		}
	}
	return live
}

func (s *Service) withState(ctx context.Context, d Database, live map[string][]agent.Database) Database {
	dbs, ok := live[d.NodeID]
	if !ok {
		address, tokenHash, err := s.nodes.AgentTarget(ctx, d.NodeID)
		if err == nil {
			dbs, err = s.agents.ListDatabases(ctx, address, tokenHash)
		}
		if err != nil {
			dbs = nil
		}
		live[d.NodeID] = dbs
	}
	if dbs == nil {
		d.NodeDown = true
		return d
	}
	d.State = "missing" // the hub has it, the node doesn't
	for _, l := range dbs {
		if l.Name == d.Name {
			d.State = l.State
		}
	}
	return d
}

// SetSchedule changes how often backups run and how many the node keeps.
func (s *Service) SetSchedule(ctx context.Context, name string, sc Schedule) (Database, error) {
	if sc.EveryHours < 0 || sc.EveryHours > 720 || sc.Keep < 1 || sc.Keep > 100 {
		return Database{}, ErrSchedule
	}
	d, err := s.store.Get(ctx, name)
	if err != nil {
		return d, err
	}
	if err := s.store.setSchedule(ctx, d.ID, sc); err != nil {
		return d, err
	}
	return s.Get(ctx, name)
}

// Backup dumps the database now. The result (ok or the error) is what the UI shows as the last backup.
func (s *Service) Backup(ctx context.Context, name string) (agent.Backup, error) {
	d, err := s.store.Get(ctx, name)
	if err != nil {
		return agent.Backup{}, err
	}
	return s.backup(ctx, d)
}

func (s *Service) backup(ctx context.Context, d Database) (agent.Backup, error) {
	address, tokenHash, err := s.nodes.AgentTarget(ctx, d.NodeID)
	if err != nil {
		return agent.Backup{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	b, err := s.agents.BackupDatabase(ctx, address, tokenHash, d.Name, d.BackupKeep)
	msg := ""
	if err != nil {
		err = agentErr(err)
		msg = err.Error()
	}
	if serr := s.store.backedUp(context.WithoutCancel(ctx), d.ID, s.now().UTC(), msg); serr != nil && err == nil {
		err = serr
	}
	return b, err
}

// Backups lists the dumps on the node, newest first.
func (s *Service) Backups(ctx context.Context, name string) ([]agent.Backup, error) {
	d, err := s.store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, d.NodeID)
	if err != nil {
		return nil, err
	}
	list, err := s.agents.ListBackups(ctx, address, tokenHash, d.Name)
	if list == nil {
		list = []agent.Backup{}
	}
	return list, agentErr(err)
}

// Download streams one dump from the node.
func (s *Service) Download(ctx context.Context, name, file string) (io.ReadCloser, int64, error) {
	if !agent.ValidBackupFile(file) {
		return nil, 0, ErrInvalidFile
	}
	d, err := s.store.Get(ctx, name)
	if err != nil {
		return nil, 0, err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, d.NodeID)
	if err != nil {
		return nil, 0, err
	}
	body, size, err := s.agents.DownloadBackup(ctx, address, tokenHash, d.Name, file)
	return body, size, agentErr(err)
}

// Restore replaces the database's data with a dump. The caller confirmed it: it can't be undone,
// short of restoring another dump.
func (s *Service) Restore(ctx context.Context, name, file string) error {
	if !agent.ValidBackupFile(file) {
		return ErrInvalidFile
	}
	d, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, d.NodeID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	return agentErr(s.agents.RestoreDatabase(ctx, address, tokenHash, d.Name, file))
}

// Link puts the connection URL in the app's environment (a secret variable). The app picks it
// up on its next deploy; it must run on the database's node, the only place it can reach it.
func (s *Service) Link(ctx context.Context, name string, in LinkInput) ([]workspace.EnvVar, error) {
	d, err := s.store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	key := in.Key
	if key == "" {
		key = map[string]string{"redis": "REDIS_URL", "valkey": "REDIS_URL", "mongo": "MONGODB_URI"}[d.Engine]
		if key == "" {
			key = "DATABASE_URL"
		}
	}
	if !names.EnvKey(key) {
		return nil, ErrEnvKey
	}
	a, err := s.apps.Get(ctx, in.App)
	if err != nil {
		return nil, err
	}
	if a.ProjectID != d.ProjectID {
		return nil, ErrOtherProj
	}
	if a.NodeID != d.NodeID {
		return nil, ErrOtherNode
	}
	d = s.withConn(d, true)
	if d.URL == "" {
		return nil, errors.New("can't open the database password: is secret.key the one this hub started with?")
	}
	vars, err := s.apps.Env(ctx, a.Name)
	if err != nil {
		return nil, err
	}
	vars = slices.DeleteFunc(vars, func(v workspace.EnvVar) bool { return v.Key == key })
	vars = append(vars, workspace.EnvVar{Key: key, Value: d.URL, Secret: true})
	if err := s.apps.SetEnv(ctx, a.Name, vars); err != nil {
		return nil, err
	}
	return s.apps.Env(ctx, a.Name)
}

// Delete stops the database on its node and forgets it. purge also removes its data and backups
// there; otherwise a database created again with the same name finds its data.
// ponytail: the new one gets a new password, and the old data keeps the old one (Postgres and
// MySQL only read the password env on first init): pass purge, or set the password by hand.
func (s *Service) Delete(ctx context.Context, name string, purge bool) error {
	d, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, d.NodeID)
	if err != nil {
		return err
	}
	if err := s.agents.DeleteDatabase(ctx, address, tokenHash, d.Name, purge); err != nil {
		if err = agentErr(err); !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return s.store.Delete(ctx, d.ID)
}

// Run backs up every database whose schedule is due, one at a time, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.RunDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunDue backs up the databases whose last backup is older than their interval. A failed one
// counts as an attempt: it's retried at the next interval, not every minute.
// ponytail: a node that's down skips its turn and the error shows in the UI.
func (s *Service) RunDue(ctx context.Context) {
	list, err := s.store.List(ctx)
	if err != nil {
		slog.Error("scheduled backups: list databases", "err", err)
		return
	}
	now := s.now().UTC()
	for _, d := range list {
		if d.BackupEveryHours == 0 {
			continue
		}
		since := d.CreatedAt
		if d.LastBackupAt != nil {
			since = *d.LastBackupAt
		}
		if now.Sub(since) < time.Duration(d.BackupEveryHours)*time.Hour {
			continue
		}
		if _, err := s.backup(ctx, d); err != nil {
			slog.Error("scheduled backup failed", "database", d.Name, "err", err)
		} else {
			slog.Info("scheduled backup done", "database", d.Name)
		}
	}
}

// agentErr maps the agent's answers to errors the handlers know.
func agentErr(err error) error {
	var ae *agent.AgentError
	if errors.As(err, &ae) {
		switch {
		case ae.Status == 404 && strings.Contains(ae.Msg, "backup"):
			return ErrNoBackup
		case ae.Status == 404:
			return ErrNotFound
		case ae.Status == 409:
			return ErrBusy
		}
	}
	return err
}
