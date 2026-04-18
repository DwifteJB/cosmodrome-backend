package imageroutes

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

type entry struct {
	data    []byte
	expires time.Time
}

type kv struct {
	id      string
	expires time.Time
}

var (
	mu      sync.RWMutex
	store   = map[string]entry{}
	baseURL string
	ttl     time.Duration
)

func randomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func detectContentType(data []byte) string {
	if len(data) >= 4 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	return "image/png"
}

// runs in bg and clears out old images every minute, and also ensures we don't have more than 100 entries in the store
func evictExpired() {
	for {
		time.Sleep(time.Minute)
		now := time.Now()
		mu.Lock()
		for id, e := range store {
			if now.After(e.expires) {
				delete(store, id)
			}
		}

		// check if mu has more than 100 entries - if so, evict the oldest ones until we have 100
		if len(store) > 100 {
			var entries []kv
			for id, e := range store {
				entries = append(entries, kv{id: id, expires: e.expires})
			}
			// sort by expires
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].expires.Before(entries[j].expires)
			})
			for i := 0; i < len(entries)-100; i++ {
				delete(store, entries[i].id)
			}
		}
		
		mu.Unlock()
	}
}

func Register(r chi.Router) {
	baseURL = os.Getenv("BASE_URL")
	if baseURL == "" {
		fmt.Fprintln(os.Stderr, "BASE_URL is required (e.g. https://cosmodrome.rmfosho.me)")
		os.Exit(1)
	}

	ttlSecs := 3600
	if s := os.Getenv("TTL_SECONDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			ttlSecs = v
		}
	}

	ttl = time.Duration(ttlSecs) * time.Second

	r.Post("/upload", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Image string `json:"image"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Image == "" {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		data, err := base64.StdEncoding.DecodeString(body.Image)
		if err != nil {
			http.Error(w, "invalid base64", http.StatusBadRequest)
			return
		}

		ext := ".jpg"
		if len(data) >= 4 && data[0] == 0x89 && data[1] == 'P' {
			ext = ".png"
		}

		id := randomID()
		mu.Lock()
		store[id] = entry{data: data, expires: time.Now().Add(ttl)}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"url": fmt.Sprintf("%s/image/%s%s", baseURL, id, ext),
		})
	})

	// {file} is the id and can end in .png or .jpg since we just ignore it
	r.Get("/image/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := chi.URLParam(r, "file")
		id := strings.TrimSuffix(strings.TrimSuffix(file, ".png"), ".jpg")

		mu.RLock()
		e, ok := store[id]
		mu.RUnlock()

		if !ok || time.Now().After(e.expires) {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", detectContentType(e.data))
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(e.data)
	})
}

func init() {
	// setup eviction, etc
	go evictExpired()
}
