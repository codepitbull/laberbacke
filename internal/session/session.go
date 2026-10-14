// Package session stores chats on disk, one directory per session UUID.
package session

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/google/uuid"
)

const (
	stateFile = ".session.json" // dotfile: hidden from the documents list

	// TranscriptFile is the chat.md transcript kept in every session.
	TranscriptFile = "chat.md"
)

// Session is one chat. It lives in <root>/<uuid>/ together with the
// documents the model created and a chat.md transcript.
type Session struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Created  time.Time `json:"created"`
	Messages []model.D `json:"messages"` // what the model sees
	Entries  []Entry   `json:"entries"`  // what the browser shows

	// Mu guards the fields above and serializes turns in this session.
	Mu sync.Mutex `json:"-"`

	dir string
}

// Entry is one chat bubble. User entries have a single text block; assistant
// entries interleave notes (tool results) and streamed text, in output order.
type Entry struct {
	Role   string  `json:"role"`
	Blocks []Block `json:"blocks"`
}

type Block struct {
	Note bool   `json:"note,omitempty"`
	Text string `json:"text"`
}

// Store finds and lists sessions below a root directory.
type Store struct {
	root string

	mu       sync.Mutex
	sessions map[string]*Session
}

func NewStore(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root, sessions: map[string]*Session{}}, nil
}

// Root is the directory holding all sessions.
func (s *Store) Root() string { return s.root }

var ErrInvalidID = errors.New("invalid session id")

// Get returns the session for id, loading it from disk if it exists there.
// Unknown but valid ids yield a new, empty session that is only written to
// disk once it has a message.
func (s *Store) Get(id string) (*Session, error) {
	if u, err := uuid.Parse(id); err != nil || u.String() != id {
		return nil, ErrInvalidID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[id]; ok {
		return sess, nil
	}

	sess := &Session{ID: id, dir: filepath.Join(s.root, id)}
	data, err := os.ReadFile(filepath.Join(sess.dir, stateFile))
	switch {
	case err == nil:
		if err := json.Unmarshal(data, sess); err != nil {
			return nil, err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}

	s.sessions[id] = sess
	return sess, nil
}

type Summary struct {
	ID      string
	Title   string
	Created time.Time
}

// List returns the sessions saved on disk, newest first.
func (s *Store) List() []Summary {
	dirs, _ := os.ReadDir(s.root)

	var out []Summary
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, d.Name(), stateFile))
		if err != nil {
			continue
		}
		var sum Summary
		if json.Unmarshal(data, &sum) == nil && sum.ID == d.Name() {
			out = append(out, sum)
		}
	}

	slices.SortFunc(out, func(a, b Summary) int { return b.Created.Compare(a.Created) })
	return out
}

// Dir is the session's directory; it may not exist before the first Save.
func (sess *Session) Dir() string { return sess.dir }

// Save writes the session state and the chat.md transcript. Callers hold
// sess.Mu.
func (sess *Session) Save() error {
	if err := os.MkdirAll(sess.dir, 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sess.dir, stateFile), data, 0o600); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(sess.dir, TranscriptFile), []byte(sess.Markdown()), 0o600)
}

// Markdown renders the chat the way the browser shows it.
func (sess *Session) Markdown() string {
	var b strings.Builder
	b.WriteString("# " + sess.Title + "\n\n")
	for _, e := range sess.Entries {
		if e.Role == model.RoleUser {
			b.WriteString("**You:**\n\n")
		} else {
			b.WriteString("**Assistant:**\n\n")
		}
		for _, bl := range e.Blocks {
			if bl.Note {
				b.WriteString("_" + bl.Text + "_\n\n")
			} else {
				b.WriteString(strings.TrimSpace(bl.Text) + "\n\n")
			}
		}
	}
	return b.String()
}

// DocNames lists the session's artifacts, skipping hidden files and the
// chat transcript.
func (sess *Session) DocNames() []string {
	entries, _ := os.ReadDir(sess.dir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != TranscriptFile {
			names = append(names, e.Name())
		}
	}
	return names
}
