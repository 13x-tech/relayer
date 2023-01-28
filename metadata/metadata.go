package metadata

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type ImageInfo struct {
	Height int    `mata:"og:image:height" json:"height,omitempty"`
	Width  int    `meta:"og:image:width" json:"width,omitempty"`
	Alt    string `meta:"og:image:alt" json:"alt,omitempty"`
	URL    string `meta:"og:image" json:"url"`
}

func getImageMeta(doc *goquery.Document) ImageInfo {
	info := ImageInfo{}
	info.Height, _ = strconv.Atoi(getMetaTag(doc, "og:image:height", ""))
	info.Width, _ = strconv.Atoi(getMetaTag(doc, "og:image:width", ""))
	info.URL = getMetaTag(doc, "og:image", "")
	info.Alt = getMetaTag(doc, "og:image:alt", "")
	return info
}

type VideoInfo struct {
	Height int    `mata:"og:image:height" json:"height,omitempty"`
	Width  int    `meta:"og:image:width" json:"width,omitempty"`
	URL    string `meta:"og:image" json:"url"`
}

func getVideoMeta(doc *goquery.Document) VideoInfo {
	info := VideoInfo{}
	info.Height, _ = strconv.Atoi(getMetaTag(doc, "og:video:height", ""))
	info.Width, _ = strconv.Atoi(getMetaTag(doc, "og:video:width", ""))
	info.URL = getMetaTag(doc, "og:video", "")
	return info
}

type ArticleMeta struct {
	Author        string    `meta:"article:author" json:"author,omitempty"`
	Publisher     string    `meta:"article:publisher" json:"publisher,omitempty"`
	Section       string    `meta:"article:section" json:"section,omitempty"`
	PublishedTime time.Time `meta:"article:published_time" json:"published,omitempty"`
	ModifiedTime  time.Time `meta:"article:modified_time" json:"modified,omitempty"`
	Tags          []string  `meta:"article:tag" json:"tags,omitempty"`
}

func getArticleMeta(doc *goquery.Document) ArticleMeta {
	meta := ArticleMeta{}
	meta.Author = getMetaTag(doc, "article:author", "")
	meta.Publisher = getMetaTag(doc, "article:publisher", "")
	meta.Section = getMetaTag(doc, "article:section", "")
	meta.PublishedTime, _ = getTimeMeta(doc, "article:published_time")
	meta.ModifiedTime, _ = getTimeMeta(doc, "article:modified_time")
	meta.Tags = strings.Split(getMetaTag(doc, "article:tag", ""), ",")
	return meta
}

func getTimeMeta(doc *goquery.Document, tag string) (time.Time, error) {
	timeString := getMetaTag(doc, tag, "")
	if len(timeString) > 0 {
		return time.Parse(time.RFC3339, timeString)
	}
	return time.Time{}, fmt.Errorf("no time string")
}

// MetaData holds the relevant meta data for a page
type MetaData struct {
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	URL         string      `json:"url,omitempty"`
	Image       ImageInfo   `json:"image,omitempty"`
	Video       VideoInfo   `json:"video,omitempty"`
	Keywords    []string    `json:"keywords,omitempty"`
	Article     ArticleMeta `json:"articleMeta,omitempty"`
}

func getCanonicalLink(doc *goquery.Document) string {
	found := doc.Find("head link[rel='canonical']").First()
	if found.Length() > 0 {
		return found.AttrOr("href", "")
	}
	return ""
}

func getMetaTag(doc *goquery.Document, tag, defaultVal string) string {
	return getMeta(doc, "property", tag, getMeta(doc, "name", tag, defaultVal))
}

func getMeta(doc *goquery.Document, attribute, tag, defaultVal string) string {
	found := doc.Find(fmt.Sprintf("head meta[%s='%s']", attribute, tag)).First()
	if found.Length() > 0 {
		return found.AttrOr("content", defaultVal)
	}
	return defaultVal
}

func fixURL(url string) string {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}

	if strings.HasPrefix(url, "http:/") {
		return strings.Replace(url, "http:/", "http://", 1)
	}

	if strings.HasPrefix(url, "https:/") {
		return strings.Replace(url, "https:/", "https://", 1)
	}
	return url
}

func FetchMetaData(url string) (*MetaData, error) {
	fmt.Printf("Fetch Metadata: %s\n", url)
	url = fixURL(url)

	r, err := http.NewRequest(
		http.MethodHead,
		url,
		bytes.NewReader([]byte{}),
	)
	if err != nil {
		return nil, err
	}

	r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/89.0.4389.82 Safari/537.36")
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;")
	r.Header.Set("Accept-Language", "en-US,en;q=0.5")
	r.Header.Set("Cache-Control", "max-age=0")

	res, err := http.DefaultClient.Do(r)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Got HEAD: %s\n", url)

	contentTyp := res.Header.Get("content-type")
	switch true {
	case strings.Contains(contentTyp, "text/html"):
	case strings.Contains(contentTyp, "application/xhtml+xml"):
	case strings.Contains(contentTyp, "application/xml"):
		break
	default:
		return nil, fmt.Errorf("invalid format")
	}

	r, err = http.NewRequest(
		http.MethodGet,
		fixURL(url),
		bytes.NewReader([]byte{}),
	)
	if err != nil {
		return nil, err
	}

	r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/89.0.4389.82 Safari/537.36")
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	r.Header.Set("Accept-Encoding", "gzip, deflate, br")
	r.Header.Set("Accept-Language", "en-US,en;q=0.5")
	r.Header.Set("Cache-Control", "max-age=0")

	res, err = http.DefaultClient.Do(r)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Got Get: %s\n", url)

	defer res.Body.Close()

	if res.StatusCode != 200 {
		return nil, fmt.Errorf("status code %d error: %s", res.StatusCode, res.Status)
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return nil, err
	}

	metaData := &MetaData{}
	metaData.URL = getMetaTag(doc, "og:url", getCanonicalLink(doc))
	metaData.Title = getMetaTag(doc, "og:title", doc.Find("head title").First().Text())
	metaData.Description = getMetaTag(doc, "og:description", getMetaTag(doc, "description", ""))
	metaData.Keywords = strings.Split(getMetaTag(doc, "keywords", ""), ",")

	metaData.Image = getImageMeta(doc)
	metaData.Video = getVideoMeta(doc)
	metaData.Article = getArticleMeta(doc)

	return metaData, nil
}
