// Package launchers reads Steam and Epic Games manifests so games installed
// through them get proper names (and Steam app ids for posters) even when
// they are not in the built in database.
package launchers

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// KV is a node of a Valve KeyValues (VDF/ACF) document.
type KV struct {
	Key      string
	Value    string
	Children []*KV
}

// Get returns the first child with the key (case insensitive).
func (kv *KV) Get(key string) *KV {
	if kv == nil {
		return nil
	}
	for _, c := range kv.Children {
		if strings.EqualFold(c.Key, key) {
			return c
		}
	}
	return nil
}

// String returns the value of a child, or "".
func (kv *KV) String(key string) string {
	if c := kv.Get(key); c != nil {
		return c.Value
	}
	return ""
}

// ParseVDF parses the text KeyValues format used by libraryfolders.vdf and
// appmanifest_*.acf. It returns a synthetic root node.
func ParseVDF(r io.Reader) (*KV, error) {
	br := bufio.NewReader(r)
	root := &KV{}
	stack := []*KV{root}
	var pendingKey *string
	for {
		tok, isStr, err := nextToken(br)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		cur := stack[len(stack)-1]
		switch {
		case !isStr && tok == "{":
			if pendingKey == nil {
				return nil, fmt.Errorf("vdf: '{' without a key")
			}
			n := &KV{Key: *pendingKey}
			cur.Children = append(cur.Children, n)
			stack = append(stack, n)
			pendingKey = nil
		case !isStr && tok == "}":
			if len(stack) == 1 {
				return nil, fmt.Errorf("vdf: unbalanced '}'")
			}
			if pendingKey != nil {
				cur.Children = append(cur.Children, &KV{Key: *pendingKey})
				pendingKey = nil
			}
			stack = stack[:len(stack)-1]
		default:
			if pendingKey == nil {
				k := tok
				pendingKey = &k
			} else {
				cur.Children = append(cur.Children, &KV{Key: *pendingKey, Value: tok})
				pendingKey = nil
			}
		}
	}
	if len(stack) != 1 {
		return nil, fmt.Errorf("vdf: unexpected end of file")
	}
	return root, nil
}

func nextToken(br *bufio.Reader) (string, bool, error) {
	for {
		c, err := br.ReadByte()
		if err != nil {
			return "", false, err
		}
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			continue
		case c == '/':
			n, err := br.ReadByte()
			if err == nil && n == '/' {
				if _, err := br.ReadString('\n'); err != nil && err != io.EOF {
					return "", false, err
				}
				continue
			}
			if err == nil {
				_ = br.UnreadByte()
			}
			return readBare(br, c)
		case c == '{' || c == '}':
			return string(c), false, nil
		case c == '"':
			var b strings.Builder
			for {
				c, err := br.ReadByte()
				if err != nil {
					return "", false, fmt.Errorf("vdf: unterminated string")
				}
				if c == '\\' {
					n, err := br.ReadByte()
					if err != nil {
						return "", false, fmt.Errorf("vdf: unterminated string")
					}
					switch n {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(n)
					}
					continue
				}
				if c == '"' {
					return b.String(), true, nil
				}
				b.WriteByte(c)
			}
		case c == '[':
			// conditional like [$WIN32], skip it
			if _, err := br.ReadString(']'); err != nil {
				return "", false, err
			}
			continue
		default:
			return readBare(br, c)
		}
	}
}

func readBare(br *bufio.Reader, first byte) (string, bool, error) {
	b := []byte{first}
	for {
		c, err := br.ReadByte()
		if err != nil {
			return string(b), true, nil
		}
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '{' || c == '}' || c == '"' {
			_ = br.UnreadByte()
			return string(b), true, nil
		}
		b = append(b, c)
	}
}
