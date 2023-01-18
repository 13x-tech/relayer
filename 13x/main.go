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
	serviceURL string `envconfig:"SERVICE_URL"`
	maxSize    string `envconfig:"MAX_SIZE"`

	PostgresDatabase string   `envconfig:"POSTGRESQL_DATABASE"`
	WhiteList        []string `envconfig:"WHITELIST"`

	storage *postgresql.PostgresBackend
}

func (r *Relay) Name() string {
	return "BasicRelay"
}

func (r *Relay) MaxSize() int {
	max, err := strconv.Atoi(r.maxSize)
	if err != nil {
		return DEFAULT_MAX_SIZE
	}

	return max
}

func (r *Relay) Storage() relayer.Storage {
	return r.storage
}

func (r *Relay) ServiceURL() string {
	return r.serviceURL
}

func (r *Relay) OnInitialized(*relayer.Server) {
	log.Printf("On Initialized\n")
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
			db.DB.Exec(`DELETE FROM event WHERE created_at < $1`, time.Now().AddDate(0, -3, 0).Unix()) // 3 months
		}
	}()

	return nil
}

func (r *Relay) AcceptEvent(evt *nostr.Event) bool {

	for _, pub := range r.WhiteList {
		if !strings.EqualFold(pub, evt.PubKey) {
			return false
		}
	}

	// block events that are too large
	jsonb, _ := json.Marshal(evt)
	return len(jsonb) <= r.MaxSize()
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

func main() {
	r := Relay{}
	if err := envconfig.Process("", &r); err != nil {
		log.Fatalf("failed to read from env: %v", err)
		return
	}

	defaultWhitelist := []string{
		"fdec23b0d7ed7dc0dd3f36a5893cd59a0f85f28fb5db24f2c7a74ee2b693ad7c",
		"a9e5bff17ded4a4a3bf4de3ff7be295ca85678ac4f9dc647a1c3829f52e65299",
		"96cfbb4951087d73758cfb237521d70caf6cb05bb07312cedda92652ce879ece",
		"20ad58ee66f3cd0ea57a549c79a395a2c889e16467d2577f725e0e7abe680920",
	}

	r.WhiteList = append(r.WhiteList, defaultWhitelist...)

	r.storage = &postgresql.PostgresBackend{DatabaseURL: r.PostgresDatabase}
	if err := relayer.Start(&r); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}
