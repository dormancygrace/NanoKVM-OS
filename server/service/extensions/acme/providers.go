package acme

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ProviderOption is one credential or setting a DNS hook reads from the
// environment.
type ProviderOption struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Provider describes a DNS hook from the dns_<hook>_info block at the top of
// its script (the same block the acme.sh wiki is generated from).
type Provider struct {
	Hook string `json:"hook"`
	Name string `json:"name"`
	Site string `json:"site,omitempty"`
	Docs string `json:"docs,omitempty"`
	// Options is the main credential set; OptionsAlt an alternative one.
	Options    []ProviderOption `json:"options"`
	OptionsAlt []ProviderOption `json:"optionsAlt,omitempty"`
}

// envNames is the set of variables a hook may be given.
func (p Provider) envNames() map[string]bool {
	names := map[string]bool{}
	for _, o := range p.Options {
		names[o.Name] = true
	}
	for _, o := range p.OptionsAlt {
		names[o.Name] = true
	}
	return names
}

var hookName = regexp.MustCompile(`^dns_[a-z0-9_]+$`)

// ListProviders reads every installed hook.
func ListProviders() ([]Provider, error) {
	return listProviders(hooksDir())
}

func listProviders(dir string) ([]Provider, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "dns_*.sh"))
	if err != nil {
		return nil, err
	}
	providers := make([]Provider, 0, len(paths))
	for _, path := range paths {
		hook := strings.TrimSuffix(filepath.Base(path), ".sh")
		if !hookName.MatchString(hook) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		providers = append(providers, parseProviderInfo(hook, data))
	}
	sort.Slice(providers, func(i, j int) bool {
		return strings.ToLower(providers[i].Name) < strings.ToLower(providers[j].Name)
	})
	return providers, nil
}

// provider finds one installed hook.
func provider(hook string) (Provider, bool) {
	if !hookName.MatchString(hook) {
		return Provider{}, false
	}
	data, err := os.ReadFile(filepath.Join(hooksDir(), hook+".sh"))
	if err != nil {
		return Provider{}, false
	}
	return parseProviderInfo(hook, data), true
}

// parseProviderInfo reads the dns_<hook>_info='...' block: the first line is
// the name, "Key: value" lines give Site and Docs, and the indented lines
// under "Options:" and "OptionsAlt:" are "VAR description". A script without
// the block is listed by its hook name alone.
func parseProviderInfo(hook string, script []byte) Provider {
	p := Provider{Hook: hook, Name: hook, Options: []ProviderOption{}}
	start := bytes.Index(script, []byte(hook+"_info='"))
	if start < 0 {
		return p
	}
	block := script[start+len(hook)+len("_info='"):]
	end := bytes.IndexByte(block, '\'')
	if end < 0 {
		return p
	}
	scanner := bufio.NewScanner(bytes.NewReader(block[:end]))
	first := true
	section := ""
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			first = false
			if name := strings.TrimSpace(line); name != "" {
				p.Name = name
			}
			continue
		}
		if strings.HasPrefix(line, " ") {
			name, description, _ := strings.Cut(strings.TrimSpace(line), " ")
			if name == "" {
				continue
			}
			option := ProviderOption{Name: name, Description: strings.TrimSpace(description)}
			switch section {
			case "Options":
				p.Options = append(p.Options, option)
			case "OptionsAlt":
				p.OptionsAlt = append(p.OptionsAlt, option)
			}
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		section = ""
		switch key {
		case "Options", "OptionsAlt":
			section = key
		case "Site":
			p.Site = withScheme(value)
		case "Docs":
			p.Docs = withScheme(value)
		}
	}
	return p
}

func withScheme(url string) string {
	if url == "" || strings.Contains(url, "://") {
		return url
	}
	return "https://" + url
}
