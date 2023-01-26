package media

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kelseyhightower/envconfig"
)

var allowedHeaders = []string{
	"Content-Type",
	"V-Filename",
	"V-Content-Type",
	"V-Description",
	"V-Full-Digest",
}

type Server struct {
	Path        string `envconfig:"MEDIA"`
	Host        string `envconfig:"MEDIA_HOST"`
	Port        string `envconfig:"MEDIA_PORT"`
	OriginUrl   string `envconfig:"ORIGIN_URL"`
	MediaDomain string `envconfig:"MEDIA_DOMAIN"`
	MediaDir    string `envconfig:"MEDIA_DIR"`
}

func (s Server) Start(close chan error) error {

	if err := envconfig.Process("", &s); err != nil {
		return fmt.Errorf("envconfig: %w", err)
	}

	s.OriginUrl = ""

	if len(s.MediaDir) == 0 {
		s.MediaDir = os.TempDir()
	}

	if len(s.Port) == 0 {
		s.Port = "9191"
	}

	http.HandleFunc("/media/", s.HandleMedia)
	http.HandleFunc("/upload", s.UploadHandler)

	go func() {
		log.Printf("Listening on: %s\n", fmt.Sprintf("%s:%s", s.Host, s.Port))
		err := http.ListenAndServe(fmt.Sprintf("%s:%s", s.Host, s.Port), nil)
		close <- err
	}()

	return nil
}

func (s *Server) HandleMedia(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.setCORSHeaders(rw)

		if strings.Contains(r.URL.Path, "index.html") {
			rw.WriteHeader(400)
			return
		}

		path := strings.Trim(r.URL.Path, "/")
		path = s.MediaDir + "/" + path
		fmt.Printf("get path: %s\n", path)
		stat, err := os.Stat(path)
		if err != nil {
			rw.WriteHeader(404)
			return
		}

		if stat.IsDir() {
			rw.WriteHeader(404)
			return
		}

		http.ServeFile(rw, r, path)
		return
	}

	rw.WriteHeader(400)
}

func (s *Server) UploadHandler(rw http.ResponseWriter, r *http.Request) {
	if r.ContentLength > 25000000 {
		rw.WriteHeader(413)
		return
	}

	if ok := s.handleCORS(rw, r); ok {
		return
	}

	s.setCORSHeaders(rw)

	if s.handleVoidCat(rw, r) {
		return
	}

	log.Printf("Not Voidcat")
}

func (s *Server) handleVoidCat(rw http.ResponseWriter, r *http.Request) bool {
	var errMessage string

	contentType := r.Header.Get("Content-Type")
	if contentType != "application/octet-stream" {
		return false
	}

	vFileName := r.Header.Get("V-Filename")
	if len(vFileName) == 0 {
		return false
	}
	fileBase := filepath.FromSlash(vFileName)
	fileExt := filepath.Ext(vFileName)
	fileName := fileBase[:len(fileBase)-len(fileExt)]

	vContentType := r.Header.Get("V-Content-Type")
	if len(vContentType) == 0 {
		return false
	}

	defer r.Body.Close()

	mediadir := fmt.Sprintf("%s/media", s.MediaDir)
	fmt.Printf("Media Dir: %s\n", mediadir)
	finalFile, err := ioutil.TempFile(mediadir, fmt.Sprintf("%s-*%s", fileName, fileExt))
	if err != nil {
		errMessage = fmt.Sprintf("could not create permanent file")
		log.Printf("could not create permanent file: %s", err)
		s.writeCatResponse(rw, false, "", errMessage)
		return true
	}

	defer finalFile.Close()

	fileBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		errMessage = fmt.Sprintf("could copy permanent file")
		fmt.Printf("could not copy permanent file: %s", err)
		s.writeCatResponse(rw, false, "", errMessage)
		return true
	}

	if _, err := finalFile.Write(fileBytes); err != nil {
		errMessage = fmt.Sprintf("could write permanent file")
		fmt.Printf("could not write permanent file: %s", err)
		s.writeCatResponse(rw, false, "", errMessage)
		return true
	}
	fileParts := strings.Split(finalFile.Name(), "/")
	fileName = fmt.Sprintf("media/%s", fileParts[len(fileParts)-1])
	s.writeCatResponse(rw, true, fileName, "")
	return true
}

func (s *Server) writeCatResponse(rw http.ResponseWriter, ok bool, fileId, errorMessage string) {
	var returnObj struct {
		Ok   bool `json:"ok"`
		File struct {
			Id   string `json:"id"`
			Meta struct {
				Url string `json:"url"`
			} `json:"meta,omitempty"`
		} `json:"file,omitempty"`
		ErrorMessage *string `json:"errorMessage,omitempty"`
	}

	if ok && len(fileId) > 0 {
		returnObj.Ok = ok
		returnObj.File.Id = fileId
		returnObj.File.Meta.Url = fmt.Sprintf(`%s/%s`, strings.TrimSuffix(s.MediaDomain, "/"), fileId)
	} else if len(errorMessage) > 0 {
		returnObj.Ok = false
		returnObj.ErrorMessage = &errorMessage
	}

	jsonResp, err := json.Marshal(returnObj)
	if err != nil {
		log.Printf("could not format response: %s", err)
		return
	}

	rw.Header().Set("Content-Type", "application/json")

	rw.WriteHeader(200)
	if _, err := rw.Write(jsonResp); err != nil {
		log.Printf("could not write response: %s", err)
	}
}

func (s *Server) setCORSHeaders(rw http.ResponseWriter) {
	fmt.Printf("Check Origin %s\n", s.OriginUrl)
	if s.OriginUrl == "" {
		rw.Header().Set("Access-Control-Allow-Headers", "*")
		rw.Header().Set("Access-Control-Allow-Origin", "*")
	} else {
		rw.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ","))
		rw.Header().Set("Access-Control-Allow-Origin", strings.TrimSuffix(s.OriginUrl, "/"))
	}
}

func (s *Server) handleCORS(rw http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodOptions {
		s.setCORSHeaders(rw)
		rw.WriteHeader(200)
		return true
	}
	return false
}
