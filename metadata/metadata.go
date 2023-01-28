package metadata

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type ImageInfo struct {
	Height int    `mata:"og:image:height" json:"height"`
	Width  int    `meta:"og:image:width" json:"width"`
	Alt    string `meta:"og:image:alt" json:"alt"`
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
	Height int    `mata:"og:image:height" json:"height"`
	Width  int    `meta:"og:image:width" json:"width"`
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
	Author        string    `meta:"article:author" json:"author"`
	Publisher     string    `meta:"article:publisher" json:"publisher"`
	Section       string    `meta:"article:section" json:"section"`
	PublishedTime time.Time `meta:"article:published_time" json:"published"`
	ModifiedTime  time.Time `meta:"article:modified_time" json:"modified"`
	Tags          []string  `meta:"article:tag" json:"tags"`
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
	Title       string      `json:"title"`
	Description string      `json:"description"`
	URL         string      `json:"url"`
	Image       ImageInfo   `json:"image,omitempty"`
	Video       VideoInfo   `json:"video,omitempty"`
	Keywords    []string    `json:"keywords"`
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
	res, err := http.Get(fixURL(url))
	if err != nil {
		return nil, err
	}

	//TODO HEAD first
	if !strings.Contains(res.Header.Get("content-type"), "text/html") {
		return nil, fmt.Errorf("invalid format")
	}

	fmt.Printf("content lengt: %d\n", res.ContentLength)
	fmt.Printf("content type: %s\n", res.Header.Get("content-type"))
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return nil, fmt.Errorf("status code error: %d %s", res.StatusCode, res.Status)
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
