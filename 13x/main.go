package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/fiatjaf/relayer"
	"github.com/fiatjaf/relayer/13x/media"
	"github.com/fiatjaf/relayer/storage/postgresql"
	"github.com/kelseyhightower/envconfig"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

const (
	DEFAULT_MAX_SIZE = 10000
)

type Relay struct {
	WSURL  string   `envconfig:"SERVICE_URL"`
	MSize  string   `envconfig:"MAX_SIZE"`
	Relays []string `envconfig:"RELAYS"`
	LNURLP string   `envconfig:"LNURLP"`

	PostgresDatabase string `envconfig:"POSTGRESQL_DATABASE"`

	allowMu sync.Mutex
	allowed map[string]bool

	storage *postgresql.PostgresBackend
	events  chan nostr.Event
	close   chan error
}

func (r *Relay) Name() string {
	return "Newstr"
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

func (r *Relay) PayURL() string {
	return r.LNURLP
}

func (r *Relay) ServiceURL() string {
	return r.WSURL
}

type LNURLHookBody struct {
	Amount  int    `json:"amount"`
	Comment string `json:"comment"`
}

func (s *Relay) handlePayment(w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("could not read body: %s\n", err)
		w.WriteHeader(500)
		return
	}

	var body LNURLHookBody
	if err := json.Unmarshal(b, &body); err != nil {
		log.Printf("could not unmarshal body: %s\n", err)
		w.WriteHeader(500)
		return
	}
	log.Printf("Payment Hook recieved: %s\n", body.Comment)

	defer w.WriteHeader(200)

	if body.Amount >= 10000000 {
		//Parse Comment NPUb into HexPubKey

		hrp, pubkey, err := nip19.Decode(body.Comment)
		if err != nil {
			log.Printf("could not decode bech32: %s\n", err)
			return
		}

		if hrp != "npub" {
			log.Printf("HRP is not npb: %s\n", hrp)
			return
		}

		if len(pubkey.(string)) < 32 {
			log.Printf("pubkey is less than 32 bytes: %d\n", len(pubkey.(string)))
			return
		}
		log.Printf("Adding %s\n", body.Comment)

		if err := s.InsertAllowed(pubkey.(string)); err != nil {
			log.Printf("could not insert into allow list: %s\n", err)
			return
		}
		s.QueryAllowed()
		log.Printf("payment success: %s\n", pubkey.(string))
	}
}

func (r *Relay) OnInitialized(s *relayer.Server) {
	s.Router().Path("/paid").HandlerFunc(r.handlePayment)
}

func (r *Relay) Init() error {

	r.events = make(chan nostr.Event)

	err := envconfig.Process("", r)
	if err != nil {
		return fmt.Errorf("couldn't process envconfig: %w", err)
	}

	db := r.Storage().(*postgresql.PostgresBackend)
	// every minute query allowlist
	go func() {
		_, err := db.DB.Exec(`
		CREATE TABLE IF NOT EXISTS allowlist (
			pubkey text NOT NULL,
			created_at integer NOT NULL
		);

		CREATE UNIQUE INDEX IF NOT EXISTS allowpubkeyprefix ON allowlist USING btree (pubkey text_pattern_ops);
		`)
		if err != nil {
			fmt.Printf("Create DB err: %s\n", err)
		}

		for {
			r.QueryAllowed()
			time.Sleep(60 * time.Second)
		}
	}()

	// every hour, delete all very old events
	go func() {

		for {
			time.Sleep(60 * time.Minute)
			db.DB.Exec(`DELETE FROM event WHERE created_at < $1`, time.Now().AddDate(0, -12, 0).Unix()) // 12 months
		}
	}()

	mediaServer := media.Server{}
	r.close = make(chan error)
	if err := mediaServer.Start(r.close); err != nil {
		return err
	}

	return nil
}

func (r *Relay) InsertAllowed(pubkey string) error {
	db := r.Storage().(*postgresql.PostgresBackend)
	if _, err := db.DB.Exec(`INSERT INTO allowlist VALUES ($1, $2) ON CONFLICT (pubkey) DO NOTHING`, pubkey, time.Now().Unix()); err != nil {
		return fmt.Errorf("failed to insert into allowlist %s: %w", pubkey, err)
	}
	return nil
}

func (r *Relay) QueryAllowed() (map[string]bool, error) {
	db := r.Storage().(*postgresql.PostgresBackend)
	query := db.DB.Rebind(`SELECT pubkey FROM allowlist ORDER BY created_at DESC;`)

	rows, err := db.DB.Query(query)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to fetch allowlist %q: %w", query, err)
	}

	defer rows.Close()

	allowed := map[string]bool{}

	for rows.Next() {
		var pub string
		err := rows.Scan(&pub)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		allowed[pub] = true
	}
	r.allowMu.Lock()
	defer r.allowMu.Unlock()

	r.allowed = allowed
	log.Printf("Allowed %d users\n", len(allowed))
	return allowed, nil
}

func (r *Relay) AcceptEvent(evt *nostr.Event) bool {
	r.allowMu.Lock()
	allowed, ok := r.allowed[evt.PubKey]
	r.allowMu.Unlock()
	if !ok {
		return false
	}

	fmt.Printf("allowed: %s\n", evt.PubKey)

	// block events that are too large
	jsonb, _ := json.Marshal(evt)
	return allowed && len(jsonb) <= r.MaxSize()
}

func (r *Relay) advertisePayEvent(ws *relayer.WebSocket, request []json.RawMessage) {

	// var id string
	// json.Unmarshal(request[1], &id)

	// event := nostr.Event{
	// 	ID:        "1",
	// 	PubKey:    "12345",
	// 	CreatedAt: time.Now(),
	// 	Kind:      22241,
	// 	Content:   `<a href="lightning:LNURL1DP68GURN8GHJ7MR9VAJKUEPWD3HXY6T5WVHXXMMD9AKXUATJD3CZ7JJTTFQKY4QWT8J97"> LNURL Pay </a>`,
	// }
	// ws.WriteJSON([]interface{}{"EVENT", id, event})

}

func (r *Relay) RequestRecieved(ws *relayer.WebSocket, request []json.RawMessage) bool {
	r.allowMu.Lock()
	allowed, ok := r.allowed[ws.Authed()]
	r.allowMu.Unlock()
	if !ok || !allowed {
		go r.advertisePayEvent(ws, request)
	}
	return ok && allowed
}

func (r *Relay) BeforeSave(evt *nostr.Event) {
	// do nothing
}

func (r *Relay) AfterSave(evt *nostr.Event) {
	// do nothing
}

func (s *Relay) InjectEvents() chan nostr.Event {
	return s.events
}

func main() {

	r := Relay{}
	if err := envconfig.Process("", &r); err != nil {
		log.Fatalf("failed to read from env: %v", err)
		return
	}

	r.storage = &postgresql.PostgresBackend{DatabaseURL: r.PostgresDatabase}
	if err := relayer.Start(&r); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}
