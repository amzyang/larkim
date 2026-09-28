package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// fileMode is what a configuration file this package creates is born with, and
// what one whose mode cannot be read is written back as.
const fileMode = 0o644

// SetFile writes one key into the configuration file, leaving every other key
// and the prose around it as they were. A missing file is created holding just
// that key.
//
// The document is edited as a yaml.Node rather than marshalled back out of a
// Config: a Config carries a value for every field, so marshalling one would
// write the whole schema into a file the reader keeps short, and drop the
// comments they keep beside each key.
//
// The value is not validated here. Config.Set is what judges it, and a caller
// that skips it writes whatever it was handed.
func SetFile(path, key, value string) error {
	path = Resolve(path)
	doc, err := readDoc(path)
	if err != nil {
		return err
	}
	m := doc.Content[0]
	parts := strings.Split(key, ".")
	for _, p := range parts[:len(parts)-1] {
		m = section(m, p)
	}
	node, err := valueNode(value)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	putChild(m, parts[len(parts)-1], node)
	return writeDoc(path, doc)
}

// readDoc parses the file into a document that always has a mapping under it,
// so every caller can descend without asking whether the file was there.
func readDoc(path string) (*yaml.Node, error) {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var doc yaml.Node
	if len(b) > 0 {
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		// An empty file, or one holding nothing but comments, parses to a
		// document with no mapping to hang a key on.
		head := doc.HeadComment
		doc = yaml.Node{Kind: yaml.DocumentNode, HeadComment: head,
			Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	return &doc, nil
}

// section returns the mapping a dotted key's next part lives under, creating it
// when the file has no such section yet.
func section(m *yaml.Node, key string) *yaml.Node {
	if v := child(m, key); v != nil {
		if v.Kind != yaml.MappingNode {
			*v = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map",
				HeadComment: v.HeadComment, LineComment: v.LineComment, FootComment: v.FootComment}
		}
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	appendChild(m, key, v)
	return v
}

// putChild replaces the value under key, in place so the key node keeps the
// comment block a reader wrote above it.
func putChild(m *yaml.Node, key string, node *yaml.Node) {
	v := child(m, key)
	if v == nil {
		appendChild(m, key, node)
		return
	}
	node.HeadComment, node.LineComment, node.FootComment = v.HeadComment, v.LineComment, v.FootComment
	*v = *node
}

func child(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func appendChild(m *yaml.Node, key string, node *yaml.Node) {
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
}

// valueNode parses the value the way the file's own decoder would, so the key
// lands with the tag it would have had if the reader had typed it there.
func valueNode(value string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(value), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}, nil
	}
	return doc.Content[0], nil
}

// writeDoc renders the document beside the target and renames it over, so a
// reader who opens the file mid-write reads one whole version or the other.
func writeDoc(path string, doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(fileMode)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".larkim-config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
