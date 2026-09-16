package users

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Key adalah satu public key SSH.
type Key struct {
	Line        string // baris asli
	Options     string // mis. from="10.0.0.0/8",no-port-forwarding
	Type        string
	Comment     string
	Fingerprint string // SHA256:...
	Err         error  // baris tidak valid
}

// Short adalah ringkasan key untuk ditampilkan.
func (k Key) Short() string {
	c := k.Comment
	if c == "" {
		c = "(tanpa komentar)"
	}
	return fmt.Sprintf("%s %s %s", strings.TrimPrefix(k.Type, "ssh-"), c, k.Fingerprint)
}

var keyTypes = []string{"ssh-ed25519", "ssh-rsa", "ssh-dss", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
	"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com"}

func isKeyType(s string) bool {
	for _, t := range keyTypes {
		if s == t {
			return true
		}
	}
	return false
}

// ParseKey mem-parse satu baris authorized_keys atau file .pub dan memverifikasi isinya.
func ParseKey(line string) (Key, error) {
	k := Key{Line: strings.TrimSpace(line)}
	fields := splitRespectingQuotes(k.Line)
	idx := -1
	for i, f := range fields {
		if isKeyType(f) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return k, errors.New("jenis key tidak dikenal (harus diawali ssh-ed25519, ssh-rsa, atau ecdsa-sha2-...)")
	}
	if idx+1 >= len(fields) {
		return k, errors.New("isi key kosong")
	}
	k.Options = strings.Join(fields[:idx], " ")
	k.Type = fields[idx]
	blob, err := base64.StdEncoding.DecodeString(fields[idx+1])
	if err != nil {
		return k, errors.New("isi key rusak (bukan base64); mungkin terpotong saat di-copy")
	}
	// Blob diawali string panjang-4-byte berisi jenis key yang sama.
	if len(blob) < 4 {
		return k, errors.New("isi key terlalu pendek")
	}
	n := binary.BigEndian.Uint32(blob[:4])
	if int(n)+4 > len(blob) || string(blob[4:4+n]) != k.Type {
		return k, errors.New("isi key tidak cocok dengan jenisnya; mungkin terpotong saat di-copy")
	}
	sum := sha256.Sum256(blob)
	k.Fingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	if idx+2 < len(fields) {
		k.Comment = strings.Join(fields[idx+2:], " ")
	}
	return k, nil
}

func splitRespectingQuotes(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// ParseAuthorizedKeys mem-parse isi authorized_keys. Baris kosong & komentar dilewati; baris rusak
// tetap dikembalikan dengan Err supaya bisa ditampilkan.
func ParseAuthorizedKeys(s string) []Key {
	var keys []Key
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, err := ParseKey(line)
		k.Err = err
		keys = append(keys, k)
	}
	return keys
}

// PermProblem adalah masalah izin file yang membuat sshd menolak key (StrictModes).
type PermProblem struct {
	Path   string
	Detail string
}

// CheckPermissions memeriksa izin home, ~/.ssh, dan authorized_keys untuk user.
func CheckPermissions(home string, uid int) []PermProblem {
	var probs []PermProblem
	check := func(path string, maxPerm os.FileMode, label string) {
		st, err := os.Stat(path)
		if err != nil {
			return
		}
		if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != uid && sys.Uid != 0 {
			probs = append(probs, PermProblem{path, fmt.Sprintf("%s bukan milik user ini (pemilik UID %d)", label, sys.Uid)})
		}
		if st.Mode().Perm()&^maxPerm != 0 {
			probs = append(probs, PermProblem{path, fmt.Sprintf("izin %s %04o terlalu longgar (maksimal %04o)", label, st.Mode().Perm(), maxPerm)})
		}
	}
	check(home, 0o755, "folder home")
	check(filepath.Join(home, ".ssh"), 0o700, "~/.ssh")
	check(filepath.Join(home, ".ssh", "authorized_keys"), 0o644, "authorized_keys")
	return probs
}

// ReadAuthorizedKeys membaca authorized_keys user; error izin dikembalikan apa adanya.
func ReadAuthorizedKeys(home string) ([]Key, error) {
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseAuthorizedKeys(string(data)), nil
}

// OwnKey adalah pasangan key milik user saat ini di ~/.ssh.
type OwnKey struct {
	Private string
	Public  Key
}

// ReadOwnKeys mendaftar key pribadi user saat ini (file *.pub di ~/.ssh).
func ReadOwnKeys(home string) []OwnKey {
	matches, _ := filepath.Glob(filepath.Join(home, ".ssh", "*.pub"))
	var out []OwnKey
	for _, pub := range matches {
		data, err := os.ReadFile(pub)
		if err != nil {
			continue
		}
		k, err := ParseKey(string(data))
		if err != nil {
			continue
		}
		out = append(out, OwnKey{Private: strings.TrimSuffix(pub, ".pub"), Public: k})
	}
	return out
}
