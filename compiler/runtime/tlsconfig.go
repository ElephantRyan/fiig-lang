package runtime

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// ADR 0086: the CAs a TLS connection trusts beyond the system's, and the
// client certificates it presents, configured on Socket in the
// capabilities file and reread when their files change.

// ClientCertificate is one clientCertificates entry: a certificate and key
// presented only to the hosts listed (§4).
type ClientCertificate struct {
	Hosts       []string
	Certificate string
	Key         string
}

// TLSSettings is Socket's TLS configuration (§1).
type TLSSettings struct {
	trust       []string
	systemRoots bool
	clients     []ClientCertificate

	mu    sync.Mutex
	files map[string]watchedFile
	roots *x509.CertPool
	certs []*tls.Certificate // by clients' index
}

// watchedFile is a file's last good contents and when it was changed.
type watchedFile struct {
	modified time.Time
	data     []byte
}

// NewTLSSettings reads every file now, so a missing file, PEM with no
// certificate, or a key that doesn't match its certificate is reported
// before the program starts (§6). It returns every problem found.
func NewTLSSettings(trust []string, systemRoots bool, clients []ClientCertificate) (*TLSSettings, []string) {
	s := &TLSSettings{trust: trust, systemRoots: systemRoots, clients: clients, files: map[string]watchedFile{}}
	var problems []string
	if !systemRoots && len(trust) == 0 {
		problems = append(problems, `"systemRoots": false trusts nothing unless "trust" lists a CA file`)
	}
	for _, c := range clients {
		if len(c.Hosts) == 0 {
			problems = append(problems, fmt.Sprintf("the client certificate %s lists no hosts to present it to", c.Certificate))
		}
		for _, h := range c.Hosts {
			if !validHostPattern(h) {
				problems = append(problems, fmt.Sprintf("client certificate host %q is neither a host name nor *.domain", h))
			}
		}
	}
	for _, path := range s.allFiles() {
		if _, err := s.read(path); err != nil {
			problems = append(problems, ""+err.Error())
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	if err := s.rebuild(); err != nil {
		return nil, []string{"" + err.Error()}
	}
	return s, nil
}

func (s *TLSSettings) allFiles() []string {
	paths := append([]string(nil), s.trust...)
	for _, c := range s.clients {
		paths = append(paths, c.Certificate, c.Key)
	}
	return paths
}

func validHostPattern(h string) bool {
	name := strings.TrimPrefix(h, "*.")
	if name == "" || strings.Contains(name, "*") || strings.ContainsAny(name, " /:") {
		return false
	}
	return true
}

// read returns path's contents, from the cache when its modification time
// hasn't changed.
func (s *TLSSettings) read(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, fmt.Errorf("reading %s: %v", path, err)
	}
	if f, ok := s.files[path]; ok && f.modified.Equal(info.ModTime()) {
		return f.data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %v", path, err)
	}
	s.files[path] = watchedFile{modified: info.ModTime(), data: data}
	return data, nil
}

// rebuild parses the cached files into the root pool and the client
// certificates.
func (s *TLSSettings) rebuild() error {
	var roots *x509.CertPool
	if len(s.trust) > 0 || !s.systemRoots {
		roots = x509.NewCertPool()
		if s.systemRoots {
			system, err := x509.SystemCertPool()
			if err != nil {
				return fmt.Errorf("reading the system's certificate authorities: %v", err)
			}
			roots = system
		}
		for _, path := range s.trust {
			if !appendPEMCerts(roots, s.files[path].data) {
				return fmt.Errorf("%s holds no PEM certificate", path)
			}
		}
	}
	certs := make([]*tls.Certificate, len(s.clients))
	for i, c := range s.clients {
		pair, err := tls.X509KeyPair(s.files[c.Certificate].data, s.files[c.Key].data)
		if err != nil {
			return fmt.Errorf("client certificate %s with key %s: %v", c.Certificate, c.Key, err)
		}
		certs[i] = &pair
	}
	s.roots, s.certs = roots, certs
	return nil
}

func appendPEMCerts(pool *x509.CertPool, data []byte) bool {
	found := false
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return found
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		pool.AddCert(cert)
		found = true
	}
}

// refresh rereads changed files (§5). A changed file that no longer
// parses keeps the last good configuration, logged once per change.
func (s *TLSSettings) refresh() {
	changed := false
	for _, path := range s.allFiles() {
		before := s.files[path].modified
		if _, err := s.read(path); err != nil {
			continue
		}
		changed = changed || !s.files[path].modified.Equal(before)
	}
	if !changed {
		return
	}
	roots, certs := s.roots, s.certs
	if err := s.rebuild(); err != nil {
		log.Printf("fiig: Socket: keeping the last good TLS files: %v", err)
		s.roots, s.certs = roots, certs
	}
}

// config is the tls.Config for one handshake with serverName.
func (s *TLSSettings) config(serverName string) *tls.Config {
	cfg := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if s == nil {
		return cfg
	}
	s.mu.Lock()
	s.refresh()
	roots, certs := s.roots, s.certs
	s.mu.Unlock()
	cfg.RootCAs = roots
	// §4: a certificate goes only to the hosts listed for it; with no
	// match, an empty certificate tells the server the client has none.
	cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		for i, c := range s.clients {
			for _, h := range c.Hosts {
				if hostMatches(h, serverName) {
					return certs[i], nil
				}
			}
		}
		return &tls.Certificate{}, nil
	}
	return cfg
}

// hostMatches is an exact name, or *.domain for one label below domain.
func hostMatches(pattern, host string) bool {
	pattern, host = strings.ToLower(pattern), strings.ToLower(strings.TrimSuffix(host, "."))
	if rest, ok := strings.CutPrefix(pattern, "*."); ok {
		label, domain, found := strings.Cut(host, ".")
		return found && label != "" && domain == rest
	}
	return pattern == host
}
