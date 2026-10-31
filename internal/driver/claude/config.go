package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	lockWait  = 5 * time.Second
	lockStale = 10 * time.Second
)

type document map[string]json.RawMessage

func (d *Driver) globalConfig() string {
	return filepath.Join(d.home, ".claude.json")
}

func (d *Driver) userSettings() string {
	return filepath.Join(d.home, ".claude", "settings.json")
}

func lockConfig(path string) (func(), error) {
	lock := path + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		err := os.Mkdir(lock, 0o700)
		if err == nil {
			return func() { os.Remove(lock) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if info, err := os.Stat(lock); err == nil && time.Since(info.ModTime()) > lockStale {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s is locked by another claude process", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func readDocument(path string) (document, os.FileMode, error) {
	doc := document{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return doc, 0o600, nil
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, 0, fmt.Errorf("can't read %s: %w", path, err)
	}
	return doc, info.Mode().Perm(), nil
}

func writeDocument(path string, doc document, mode os.FileMode) error {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".gofer-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(out.Bytes())
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func updateDocument(path string, change func(document) error) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, err := lockConfig(path)
	if err != nil {
		return err
	}
	defer unlock()
	doc, mode, err := readDocument(path)
	if err != nil {
		return err
	}
	if err := change(doc); err != nil {
		return err
	}
	return writeDocument(path, doc, mode)
}

func setField(doc document, key string, value bool) error {
	data, err := json.Marshal(value)
	doc[key] = data
	return err
}

func (d *Driver) Trust(dir string) error {
	return updateDocument(d.globalConfig(), func(doc document) error {
		projects := document{}
		if raw, ok := doc["projects"]; ok {
			if err := json.Unmarshal(raw, &projects); err != nil {
				return err
			}
		}
		project := document{}
		if raw, ok := projects[dir]; ok {
			if err := json.Unmarshal(raw, &project); err != nil {
				return err
			}
		}
		if err := setField(project, "hasTrustDialogAccepted", true); err != nil {
			return err
		}
		var err error
		if projects[dir], err = json.Marshal(project); err != nil {
			return err
		}
		doc["projects"], err = json.Marshal(projects)
		return err
	})
}

func (d *Driver) AcceptRemoteControl() (changed bool, err error) {
	err = updateDocument(d.globalConfig(), func(doc document) error {
		if string(doc["remoteDialogSeen"]) == "true" {
			return nil
		}
		changed = true
		return setField(doc, "remoteDialogSeen", true)
	})
	return changed, err
}

func (d *Driver) BypassAccepted() bool {
	var settings struct {
		Skip bool `json:"skipDangerousModePermissionPrompt"`
	}
	var config struct {
		Accepted bool `json:"bypassPermissionsModeAccepted"`
	}
	if data, err := os.ReadFile(d.userSettings()); err == nil {
		json.Unmarshal(data, &settings)
	}
	if data, err := os.ReadFile(d.globalConfig()); err == nil {
		json.Unmarshal(data, &config)
	}
	return settings.Skip || config.Accepted
}

func (d *Driver) AcceptBypass() error {
	return updateDocument(d.userSettings(), func(doc document) error {
		return setField(doc, "skipDangerousModePermissionPrompt", true)
	})
}
