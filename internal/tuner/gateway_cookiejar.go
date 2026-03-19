package tuner

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sync"
	"time"
)

type persistentCookieJar struct {
	file  string
	mu    sync.Mutex
	jar   http.CookieJar
	saved map[string]map[string]*httpCookie
}

type httpCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	Expires  int64  `json:"expires,omitempty"`
	HttpOnly bool   `json:"http_only,omitempty"`
}

func newPersistentCookieJar(file string) (*persistentCookieJar, error) {
	pj := &persistentCookieJar{
		file:  file,
		saved: make(map[string]map[string]*httpCookie),
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	pj.jar = jar
	if file != "" {
		if err := pj.loadFromFile(); err != nil {
			log.Printf("persistentCookieJar: load %q failed: %v (starting fresh)", file, err)
		} else {
			log.Printf("persistentCookieJar: loaded cookies from %q", file)
		}
	}
	return pj, nil
}

func (p *persistentCookieJar) Jar() http.CookieJar {
	if p == nil {
		return nil
	}
	return p.jar
}

func (p *persistentCookieJar) loadFromFile() error {
	if p == nil || p.file == "" {
		return nil
	}
	data, err := os.ReadFile(p.file)
	if err != nil {
		return err
	}
	var saved map[string]map[string]*httpCookie
	if err := json.Unmarshal(data, &saved); err != nil {
		return err
	}
	now := time.Now().Unix()
	for domain, cookies := range saved {
		for name, c := range cookies {
			if c.Expires > 0 && c.Expires < now {
				continue
			}
			p.jar.SetCookies(&url.URL{Scheme: "https", Host: domain}, []*http.Cookie{{
				Name:     c.Name,
				Value:    c.Value,
				Domain:   c.Domain,
				Path:     c.Path,
				Secure:   c.Secure,
				Expires:  time.Unix(c.Expires, 0),
				HttpOnly: c.HttpOnly,
			}})
			if p.saved[domain] == nil {
				p.saved[domain] = make(map[string]*httpCookie)
			}
			p.saved[domain][name] = c
		}
	}
	return nil
}

func (p *persistentCookieJar) Save() error {
	if p == nil || p.file == "" {
		return nil
	}
	now := time.Now().Unix()
	for domain := range p.saved {
		cookies := p.jar.Cookies(&url.URL{Scheme: "https", Host: domain})
		for _, c := range cookies {
			if c.Expires.IsZero() || c.Expires.Unix() > now {
				p.saved[domain][c.Name] = &httpCookie{
					Name:     c.Name,
					Value:    c.Value,
					Domain:   c.Domain,
					Path:     c.Path,
					Secure:   c.Secure,
					Expires:  c.Expires.Unix(),
					HttpOnly: c.HttpOnly,
				}
			}
		}
	}
	data, err := json.MarshalIndent(p.saved, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.file, data, 0600)
}
