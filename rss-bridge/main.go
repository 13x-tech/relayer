package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/fiatjaf/relayer"
	"github.com/fiatjaf/relayer/metadata"
	"github.com/fiatjaf/relayer/rss-bridge/nip13"
	"github.com/kelseyhightower/envconfig"
	"github.com/nbd-wtf/go-nostr"
	"golang.org/x/exp/slices"
)

var relay = &Relay{
	updates: make(chan nostr.Event),
}

type Relay struct {
	Secret string `envconfig:"SECRET" required:"true"`

	updates     chan nostr.Event
	lastEmitted sync.Map
	db          *pebble.DB
	mdMu        sync.RWMutex
	mdCache     map[string]*metadata.MetaData
}

func (relay *Relay) Name() string {
	return "relayer-rss-bridge"
}

func errJson(msg string, logMessage string) []byte {
	log.Println(msg)
	errJson, _ := json.Marshal(map[string]interface{}{
		"error": msg,
	})
	return errJson
}

func (relay *Relay) OnInitialized(s *relayer.Server) {

	s.Router().PathPrefix("/og/").Methods(http.MethodGet).HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Add("content-type", "application/json")

		go func(r *http.Request) {
			jsonReq, err := json.Marshal(r)
			if err != nil {
				log.Printf("[OG Triggered]: %s\n\t{}\n", r.URL.String())
			}
			log.Printf("[OG Triggered]: %s\n\t%s\n", r.URL.String(), string(jsonReq))
		}(r)

		extractedURL := strings.TrimLeft(r.URL.Path, "/og/")
		relay.mdMu.RLock()
		data, ok := relay.mdCache[extractedURL]
		relay.mdMu.RUnlock()
		if ok && data != nil {
			dataJson, _ := json.Marshal(data)
			rw.WriteHeader(200)
			rw.Write(dataJson)
			return
		}

		var err error
		data, err = metadata.FetchMetaData(extractedURL)
		if err != nil {
			if strings.Contains(err.Error(), "status code 404 error") {
				rw.WriteHeader(http.StatusNotFound)
				rw.Write([]byte(http.StatusText(http.StatusNotFound)))
				return
			}
			msg := fmt.Sprintf("could not fetch metadata %s: %s", extractedURL, err.Error())
			rw.WriteHeader(400)
			rw.Write(errJson("could nt fetch metadata", msg))
			return
		}

		dataJson, _ := json.Marshal(data)
		rw.WriteHeader(200)
		rw.Write(dataJson)

		go func(url string, data *metadata.MetaData) {

			relay.mdMu.Lock()
			relay.mdCache[extractedURL] = data
			relay.mdMu.Unlock()

			<-time.After(10 * time.Minute)
			relay.mdMu.Lock()
			delete(relay.mdCache, url)
			relay.mdMu.Unlock()

		}(extractedURL, data)
	})

	s.Router().PathPrefix("/pow/").Methods(http.MethodPost).HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Add("content-type", "application/json")

		go func(r *http.Request) {
			jsonReq, err := json.Marshal(r)
			if err != nil {
				log.Printf("[POW Triggered]: %s\n\t{}\n", r.URL.String())
			}
			log.Printf("[POW Triggered]: %s\n\t%s\n", r.URL.String(), string(jsonReq))
		}(r)

		extractedDifficulty := strings.Trim(strings.TrimLeft(r.URL.Path, "/pow/"), "/")
		difficulty, err := strconv.Atoi(extractedDifficulty)
		if err != nil {
			msg := fmt.Sprintf("could parse difficulty: %s", err.Error())
			rw.WriteHeader(400)
			rw.Write(errJson(fmt.Sprintf("could not parse difficulty: %s", extractedDifficulty), msg))
			return
		}

		fmt.Printf("[POW] Difficulty Expected: %d\n", difficulty)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			msg := fmt.Sprintf("could not read body: %s", err.Error())
			rw.WriteHeader(400)
			rw.Write(errJson("could not read body", msg))
			return
		}

		e := &nostr.Event{}
		if err := json.Unmarshal(body, e); err != nil {
			msg := fmt.Sprintf("could not unmarshal event json: %s", err.Error())
			rw.WriteHeader(400)
			rw.Write(errJson("could not unmarshal event json", msg))
			return
		}
		fmt.Printf("[POW] Event PubKey: %s\n", e.PubKey)

		nEvent, err := nip13.Generate(e, difficulty, 5*time.Minute)
		if err != nil {
			msg := fmt.Sprintf("could not generate nonce: %s", err.Error())
			rw.WriteHeader(400)
			rw.Write(errJson(msg, msg))
			return
		}
		fmt.Printf("[POW] FOUND NONCE!!: %s -  %s\n", e.ID, e.Tags)

		eventJson, err := json.Marshal(nEvent)
		if err != nil {
			msg := fmt.Sprintf("could not marshal event json: %s", err.Error())
			rw.WriteHeader(400)
			rw.Write(errJson("could not marshal event json", msg))
			return
		}

		log.Printf("!!!!!!!![RETURNED POW]!!!!!")

		rw.WriteHeader(200)
		rw.Write(eventJson)
	})
}

func (relay *Relay) Init() error {
	err := envconfig.Process("", relay)
	if err != nil {
		return fmt.Errorf("couldn't process envconfig: %w", err)
	}

	if db, err := pebble.Open("db", nil); err != nil {
		log.Fatalf("failed to open db: %v", err)
	} else {
		relay.db = db
	}

	relay.mdCache = map[string]*metadata.MetaData{}

	// 2023/01/28 04:44:24 saved feed at url "https://www.theguardian.com/us/rss" as pubkey f1440f5f94651828133f5f8f307efc2eb6053f218b546bd924595beb67c1ab9f
	// 2023/01/28 04:44:24 saved feed at url "https://www.newyorker.com/feed/posts" as pubkey 6f1658f90a18b042655c381e79ef673f91888128766a6f95f41db42a3de84db6
	// 2023/01/28 04:44:29 saved feed at url "https://www.forbes.com/lifestyle/feed" as pubkey e8e29fa47853423a4a200b8df67d8aacf032ec6f58082ae64ae161de154ebe1c
	// 2023/01/28 04:44:30 saved feed at url "https://hnrss.org/frontpage" as pubkey b02d7008b8467c5aa79a9fdca72b4dd66b8a9954a088783850a4615d9b132d29
	// 2023/01/28 04:44:30 saved feed at url "https://feeds.a.dj.com/rss/RSSWorldNews.xml" as pubkey ea5b87bc06113efdd22b3881b1f0ef44ee82b651eadb21c6c9807010ed9e68aa
	// 2023/01/28 04:44:30 saved feed at url "http://rss.cnn.com/rss/cnn_topstories.rss" as pubkey c22ab8fd0cedcdac7e491de3401964eeb6962becf5c3b65760e3ea3009416023
	// 2023/01/28 04:44:30 saved feed at url "https://moxie.foxnews.com/google-publisher/latest.xml" as pubkey 56e265a2b2e54584afd054419724b539155ba71d4ce236c26fffc7c8e4c1474a

	rssStart := []Metadata{{
		Name:    "The Guardian",
		Url:     "https://www.theguardian.com/us/rss",
		Nip05:   "guardian@newstr.id",
		Picture: "https://pbs.twimg.com/profile_images/1175141826870861825/K2qKoGla_400x400.png",
		Banner:  "https://pbs.twimg.com/profile_banners/87818409/1620214786/1500x500",
	}, {
		Name:    "The New Yorker",
		Url:     "https://www.newyorker.com/feed/posts",
		Nip05:   "newyorker@newstr.id",
		Picture: "https://pbs.twimg.com/profile_images/1226890596280885248/qdLQ8M7i_400x400.png",
		Banner:  "https://pbs.twimg.com/profile_banners/14677919/1581364143/1500x500",
	}, {
		Name:    "Washington Post",
		Url:     "https://feeds.washingtonpost.com/rss/politics",
		Nip05:   "wapo@newstr.id",
		Picture: "https://pbs.twimg.com/profile_images/1060271522319925257/fJKwJ0r2_400x400.jpg",
		Banner:  "https://pbs.twimg.com/profile_banners/2467791/1469484132/1500x500",
	}, {
		Name:    "Forbes - Lifestyle",
		Url:     "https://www.forbes.com/lifestyle/feed",
		Nip05:   "forbes@newstr.id",
		Picture: "https://pbs.twimg.com/profile_images/1577646609923543041/NcCeduIc_400x400.jpg",
		Banner:  "https://pbs.twimg.com/profile_banners/91478624/1664976352/1500x500",
	}, {
		Name:    "HackerNews",
		Url:     "https://hnrss.org/frontpage",
		Nip05:   "hn@newstr.id",
		Picture: "https://pbs.twimg.com/profile_images/1447736684331016192/9NahqH2y_400x400.png",
	}, {
		Name:    "WSJ World News",
		Url:     "https://feeds.a.dj.com/rss/RSSWorldNews.xml",
		Nip05:   "wsj@newstr.id",
		Picture: "https://pbs.twimg.com/profile_images/971415515754266624/zCX0q9d5_400x400.jpg",
		Banner:  "https://pbs.twimg.com/profile_banners/3108351/1667493390/1500x500",
	}, {
		Name:    "CNN Headline News",
		Url:     "http://rss.cnn.com/rss/cnn_topstories.rss",
		Nip05:   "cnn@newstr.id",
		Picture: "https://pbs.twimg.com/profile_images/1278259160644227073/MfCyF7CG_400x400.jpg",
		Banner:  "https://pbs.twimg.com/profile_banners/759251/1667231828/1500x500",
	}, {
		Name:    "Fox News",
		Url:     "https://moxie.foxnews.com/google-publisher/latest.xml",
		Nip05:   "fox@newstr.id",
		Picture: "https://pbs.twimg.com/profile_images/1591278197844414464/O6Fp0hFB_400x400.jpg",
		Banner:  "https://pbs.twimg.com/profile_banners/1367531/1492649996/1500x500",
	}}

	for _, feed := range rssStart {
		Feed(feed, relay.db)
	}

	go func() {
		time.Sleep(1 * time.Minute)

		filters := relayer.GetListeningFilters()
		log.Printf("checking for updates; %d filters active", len(filters))

		for _, filter := range filters {
			if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindTextNote) {
				for _, pubkey := range filter.Authors {
					if val, closer, err := relay.db.Get([]byte(pubkey)); err == nil {
						defer closer.Close()

						var entity Entity
						if err := json.Unmarshal(val, &entity); err != nil {
							log.Printf("got invalid json from db at key %s: %v", pubkey, err)
							continue
						}

						feed, err := parseFeed(entity.URL)
						if err != nil {
							log.Printf("failed to parse feed at url %q: %v", entity.URL, err)
							continue
						}

						for _, item := range feed.Items {
							evt := itemToTextNote(pubkey, item)
							last, ok := relay.lastEmitted.Load(entity.URL)
							if last == nil {
								continue
							}

							var last64 int64
							last32, ok := last.(uint32)
							if ok {
								last64 = int64(last32)
							} else {
								l64, ok := last.(int64)
								if ok {
									last64 = l64
								} else {
									log.Printf("Could not parse last")
									continue
								}
							}

							if !ok || time.Unix(last64, 0).Before(evt.CreatedAt) {
								evt.Sign(entity.PrivateKey)
								relay.updates <- evt
								relay.lastEmitted.Store(entity.URL, last)
							}
						}
					}
				}
			}
		}
	}()

	return nil
}

func (relay *Relay) AcceptEvent(_ *nostr.Event) bool {
	return false
}

func (relay *Relay) Storage() relayer.Storage {
	return store{relay.db}
}

type store struct {
	db *pebble.DB
}

func (b store) Init() error { return nil }
func (b store) SaveEvent(_ *nostr.Event) error {
	return errors.New("blocked: we don't accept any events")
}

func (b store) DeleteEvent(_, _ string) error {
	return errors.New("blocked: we can't delete any events")
}

func (b store) QueryEvents(filter *nostr.Filter) ([]nostr.Event, error) {
	var evts []nostr.Event

	if filter.IDs != nil || len(filter.Tags) > 0 {
		return evts, nil
	}

	for _, pubkey := range filter.Authors {
		if val, closer, err := relay.db.Get([]byte(pubkey)); err == nil {
			defer closer.Close()

			var entity Entity
			if err := json.Unmarshal(val, &entity); err != nil {
				log.Printf("got invalid json from db at key %s: %v", pubkey, err)
				continue
			}

			feed, err := parseFeed(entity.URL)
			if err != nil {
				log.Printf("failed to parse feed at url %q: %v", entity.URL, err)
				continue
			}

			if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindSetMetadata) {
				evt := feedToSetMetadata(pubkey, feed, entity.Meta)

				if filter.Since != nil && evt.CreatedAt.Before(*filter.Since) {
					continue
				}
				if filter.Until != nil && evt.CreatedAt.After(*filter.Until) {
					continue
				}

				evt.Sign(entity.PrivateKey)
				evts = append(evts, evt)
			}

			if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindTextNote) {
				var last uint32 = 0
				for _, item := range feed.Items {
					evt := itemToTextNote(pubkey, item)

					if filter.Since != nil && evt.CreatedAt.Before(*filter.Since) {
						continue
					}
					if filter.Until != nil && evt.CreatedAt.After(*filter.Until) {
						continue
					}

					evt.Sign(entity.PrivateKey)

					if evt.CreatedAt.After(time.Unix(int64(last), 0)) {
						last = uint32(evt.CreatedAt.Unix())
					}

					evts = append(evts, evt)
				}

				relay.lastEmitted.Store(entity.URL, last)
			}
		}
	}

	return evts, nil
}

func (relay *Relay) InjectEvents() chan nostr.Event {
	return relay.updates
}

func main() {
	if err := relayer.Start(relay); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}

type Metadata struct {
	Name    string
	Url     string
	Nip05   string
	Picture string
	Banner  string
}

func Feed(meta Metadata, db *pebble.DB) {

	feedurl := getFeedURL(meta.Url)
	if feedurl == "" {
		log.Println("couldn't find a feed url")
		return
	}

	if _, err := parseFeed(feedurl); err != nil {
		log.Printf("bad feed: %s", err.Error())
		return
	}

	sk := privateKeyFromFeed(feedurl)
	pubkey, err := nostr.GetPublicKey(sk)
	if err != nil {
		log.Printf("bad private key: %s", err.Error())
		return
	}

	j, _ := json.Marshal(Entity{
		PrivateKey: sk,
		URL:        feedurl,
		Meta:       meta,
	})

	if err := db.Set([]byte(pubkey), j, nil); err != nil {
		log.Printf("failure: %s", err.Error())
		return
	}

	log.Printf("saved feed at url %q as pubkey %s", feedurl, pubkey)
}
