package imageroutes

import (
	"crypto/rand"
	"crypto/sha256"
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

type challenge struct {
	expires time.Time
}

var (
	mu            sync.RWMutex
	store         = map[string]entry{}
	challengeMu   sync.Mutex
	challenges    = map[string]challenge{}
	baseURL       string
	ttl           time.Duration
	powDifficulty int
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

func verifyPoW(ch string, nonce int) bool {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s%d", ch, nonce)))
	for i := 0; i < powDifficulty; i++ {
		if hash[i] != 0 {
			return false
		}
	}
	return true
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
		if len(store) > 100 {
			var entries []kv
			for id, e := range store {
				entries = append(entries, kv{id: id, expires: e.expires})
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].expires.Before(entries[j].expires)
			})
			for i := 0; i < len(entries)-100; i++ {
				delete(store, entries[i].id)
			}
		}
		mu.Unlock()

		challengeMu.Lock()
		for ch, c := range challenges {
			if now.After(c.expires) {
				delete(challenges, ch)
			}
		}
		challengeMu.Unlock()
	}
}

const maxUploadBytes = 5 << 20 // 5 MB

func lookupEntry(file string) (entry, bool) {
	id := strings.TrimSuffix(strings.TrimSuffix(file, ".png"), ".jpg")
	mu.RLock()
	e, ok := store[id]
	mu.RUnlock()
	if !ok || time.Now().After(e.expires) {
		return entry{}, false
	}
	return e, true
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

	powDifficulty = 3
	if s := os.Getenv("POW_DIFFICULTY"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			powDifficulty = v
		}
	}

	r.Get("/ttl", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"ttl_seconds": ttlSecs})
	})

	// this is to prevent abuse of the upload endpoint since there's no authentication
	// PROOF OF WORK!!!
	r.Get("/challenge", func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 16)
		rand.Read(b)
		ch := fmt.Sprintf("%x", b)

		challengeMu.Lock()
		challenges[ch] = challenge{expires: time.Now().Add(5 * time.Minute)}
		challengeMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"challenge":  ch,
			"difficulty": powDifficulty,
		})
	})

	r.Post("/upload", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

		var body struct {
			Image     string `json:"image"`
			Challenge string `json:"challenge"`
			Nonce     int    `json:"nonce"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Image == "" || body.Challenge == "" {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		// consume the challenge (single-use)
		challengeMu.Lock()
		ch, ok := challenges[body.Challenge]
		if ok {
			delete(challenges, body.Challenge)
		}
		challengeMu.Unlock()

		if !ok || time.Now().After(ch.expires) {
			http.Error(w, "invalid or expired challenge", http.StatusUnauthorized)
			return
		}

		if !verifyPoW(body.Challenge, body.Nonce) {
			http.Error(w, "proof of work failed", http.StatusUnauthorized)
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

	r.Head("/image/{file}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := lookupEntry(chi.URLParam(r, "file")); !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	r.Get("/image/{file}", func(w http.ResponseWriter, r *http.Request) {
		e, ok := lookupEntry(chi.URLParam(r, "file"))
		if !ok {
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
