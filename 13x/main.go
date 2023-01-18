package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/fiatjaf/relayer"
	"github.com/fiatjaf/relayer/storage/postgresql"
	"github.com/kelseyhightower/envconfig"
	"github.com/nbd-wtf/go-nostr"
)

const (
	DEFAULT_MAX_SIZE = 10000
)

type Relay struct {
	SURL  string `envconfig:"SERVICE_URL"`
	MSize string `envconfig:"MAX_SIZE"`

	PostgresDatabase string   `envconfig:"POSTGRESQL_DATABASE"`
	Allow            []string `envconfig:"ALLOWLIST"`
	Relays           []string `envconfig:"RELAYS"`

	storage *postgresql.PostgresBackend
}

func (r *Relay) Name() string {
	return "13xRelayer"
}

func (r *Relay) MaxSize() int {
	max, err := strconv.Atoi(r.MSize)
	if err != nil {
		return DEFAULT_MAX_SIZE
	}

	return max
}

func (r *Relay) Storage() relayer.Storage {
	return r.storage
}

func (r *Relay) ServiceURL() string {
	return r.SURL
}

func (r *Relay) OnInitialized(*relayer.Server) {
	log.Printf("On Initialized\n")
}

func (r *Relay) Allowlist() []string {
	return r.Allow
}

func (r *Relay) Init() error {
	err := envconfig.Process("", r)
	if err != nil {
		return fmt.Errorf("couldn't process envconfig: %w", err)
	}

	// every hour, delete all very old events
	go func() {
		db := r.Storage().(*postgresql.PostgresBackend)

		for {
			time.Sleep(60 * time.Minute)
			db.DB.Exec(`DELETE FROM event WHERE created_at < $1`, time.Now().AddDate(0, -12, 0).Unix()) // 12 months
		}
	}()

	return nil
}

func (r *Relay) AcceptEvent(evt *nostr.Event) bool {

	var allowed = false
	for _, pub := range r.Allow {
		if strings.EqualFold(pub, evt.PubKey) {
			allowed = true
			break
		}
	}

	// block events that are too large
	jsonb, _ := json.Marshal(evt)
	return allowed && len(jsonb) <= r.MaxSize()
}

func (r *Relay) BeforeSave(evt *nostr.Event) {
	// do nothing
}

func (r *Relay) AfterSave(evt *nostr.Event) {
	// delete all but the 100 most recent ones for each key
	r.Storage().(*postgresql.PostgresBackend).DB.Exec(`DELETE FROM event WHERE pubkey = $1 AND kind = $2 AND created_at < (
      SELECT created_at FROM event WHERE pubkey = $1
      ORDER BY created_at DESC OFFSET 100 LIMIT 1
    )`, evt.PubKey, evt.Kind)
}

func (r *Relay) injestRelays() {
}

func main() {
	r := Relay{}
	if err := envconfig.Process("", &r); err != nil {
		log.Fatalf("failed to read from env: %v", err)
		return
	}

	defaultWhitelist := []string{
		"a9e5bff17ded4a4a3bf4de3ff7be295ca85678ac4f9dc647a1c3829f52e65299",
		"6f3532cc79ffddad26d57d2420b70821ab2b0a8b605a3cb159520ccdbaee001c",
	}

	defaultRelays := []string{
		"wss://relay.damus.io",
	}

	r.Allow = append(r.Allow, defaultWhitelist...)
	r.Relays = append(r.Relays, defaultRelays...)

	r.storage = &postgresql.PostgresBackend{DatabaseURL: r.PostgresDatabase}
	if err := relayer.Start(&r); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}
