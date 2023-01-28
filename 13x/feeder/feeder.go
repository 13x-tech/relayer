package feeder

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/cockroachdb/pebble"
	"github.com/fiatjaf/relayer"
	strip "github.com/grokify/html-strip-tags-go"
	"github.com/mmcdole/gofeed"
	"github.com/nbd-wtf/go-nostr"
	"github.com/rif/cache2go"
	"golang.org/x/exp/slices"
)

var client = &http.Client{
	Timeout: 5 * time.Second,
}

var feedCache = cache2go.New(512, time.Minute*19)

type Entity struct {
	PrivateKey string
	URL        string
}

var types = []string{
	"rss+xml",
	"atom+xml",
	"feed+json",
	"text/xml",
	"application/xml",
}

func getFeedURL(url string) string {

	resp, err := client.Get(url)
	if err != nil || resp.StatusCode >= 300 {
		return ""
	}

	ct := resp.Header.Get("Content-Type")
	for _, typ := range types {
		if strings.Contains(ct, typ) {
			return url
		}
	}

	if strings.Contains(ct, "text/html") {
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			return ""
		}

		for _, typ := range types {
			href, _ := doc.Find(fmt.Sprintf("link[type*='%s']", typ)).Attr("href")
			if href == "" {
				continue
			}
			if !strings.HasPrefix(href, "http") {
				href, _ = urljoin(url, href)
			}
			return href
		}
	}

	return ""
}

func urljoin(baseUrl string, elem ...string) (result string, err error) {
	u, err := url.Parse(baseUrl)
	if err != nil {
		return
	}

	if len(elem) > 0 {
		elem = append([]string{u.Path}, elem...)
		u.Path = path.Join(elem...)
	}

	return u.String(), nil
}

func parseFeed(url string) (*gofeed.Feed, error) {
	if feed, ok := feedCache.Get(url); ok {
		return feed.(*gofeed.Feed), nil
	}

	fp := gofeed.NewParser()

	feed, err := fp.ParseURL(url)
	if err != nil {
		return nil, err
	}

	// cleanup a little so we don't store too much junk
	for i := range feed.Items {
		feed.Items[i].Content = ""
	}
	feedCache.Set(url, feed)

	return feed, nil
}

func privateKeyFromFeed(secret, url string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(url))
	r := m.Sum(nil)
	return hex.EncodeToString(r)
}

func itemToTextNote(pubkey string, item *gofeed.Item) nostr.Event {
	content := ""
	if item.Title != "" {
		content = "**" + item.Title + "**\n\n"
	}
	content += strip.StripTags(item.Description)
	if len(content) > 250 {
		content += content[0:249] + "…"
	}
	content += "\n\n" + item.Link

	createdAt := time.Now()
	if item.UpdatedParsed != nil {
		createdAt = *item.UpdatedParsed
	}
	if item.PublishedParsed != nil {
		createdAt = *item.PublishedParsed
	}

	evt := nostr.Event{
		PubKey:    pubkey,
		CreatedAt: createdAt,
		Kind:      nostr.KindTextNote,
		Tags:      nostr.Tags{},
		Content:   content,
	}
	evt.ID = string(evt.Serialize())

	return evt
}

func Init(emmit sync.Map, db *pebble.DB) error {
	go func() {
		time.Sleep(20 * time.Minute)

		filters := relayer.GetListeningFilters()
		log.Printf("checking for updates; %d filters active", len(filters))

		for _, filter := range filters {
			if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindTextNote) {
				for _, pubkey := range filter.Authors {
					if val, closer, err := db.Get([]byte(pubkey)); err == nil {
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
							evt.Sign(entity.PrivateKey)

							last, ok := emmit.Load(entity.URL)
							if !ok || time.Unix(last.(int64), 0).Before(evt.CreatedAt) {
								// relay.updates <- evt
								// relay.lastEmitted.Store(entity.URL, last)
							}
						}
					}
				}
			}
		}
	}()

	return nil
}

func Feed(url, secret string, db *pebble.DB) {

	feedurl := getFeedURL(url)
	if feedurl == "" {
		log.Println("couldn't find a feed url")
		return
	}

	if _, err := parseFeed(feedurl); err != nil {
		log.Printf("bad feed: %s", err.Error())
		return
	}

	sk := privateKeyFromFeed(secret, feedurl)
	pubkey, err := nostr.GetPublicKey(sk)
	if err != nil {
		log.Printf("bad private key: %s", err.Error())
		return
	}

	j, _ := json.Marshal(Entity{
		PrivateKey: sk,
		URL:        feedurl,
	})

	if err := db.Set([]byte(pubkey), j, nil); err != nil {
		log.Printf("failure: %s", err.Error())
		return
	}

	log.Printf("saved feed at url %q as pubkey %s", feedurl, pubkey)
}
