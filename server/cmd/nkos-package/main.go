// Host-only release packager. The signing private key is never shipped.
package main

import (
	"NanoKVM-Server/osupdate"
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	app := flag.String("app", "", "staged server directory with web/ and dl_lib/")
	removeList := flag.String("remove-list", "", "optional newline-separated rootfs/... paths to remove in a system package")
	basePath := flag.String("system-base", "", "system foundation fingerprint from the release image /etc/nkos-system-base")
	system := flag.String("system", "", "optional curated rootfs directory; requires the beta-3 system updater")
	keyPath := flag.String("key", "", "private Ed25519 PKCS8 PEM (host only)")
	version := flag.String("version", "", "application version")
	sequence := flag.Uint64("sequence", 0, "strictly increasing release sequence")
	output := flag.String("output", "", "new .nkos file")
	flag.Parse()
	if *app == "" || *keyPath == "" || *output == "" {
		return fmt.Errorf("app, key, version, sequence and output required")
	}
	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return fmt.Errorf("invalid private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return fmt.Errorf("Ed25519 key required")
	}
	pub, err := osupdate.PublicKey()
	if err != nil {
		return err
	}
	if !bytes.Equal(pub, key.Public().(ed25519.PublicKey)) {
		return fmt.Errorf("private key does not match the device trust key")
	}
	abi, err := osupdate.NativeABI(filepath.Join(*app, "dl_lib"))
	if err != nil {
		return err
	}
	if err = osupdate.CheckExecutable(filepath.Join(*app, "NanoKVM-Server")); err != nil {
		return err
	}
	names := []string{"NanoKVM-Server"}
	err = filepath.WalkDir(filepath.Join(*app, "web"), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular web asset")
		}
		rel, e := filepath.Rel(*app, p)
		if e != nil {
			return e
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	if *system != "" {
		err = filepath.WalkDir(*system, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			rel, e := filepath.Rel(*system, p)
			if e != nil {
				return e
			}
			names = append(names, "rootfs/"+filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return err
		}
	}
	sort.Strings(names)
	payload, err := os.CreateTemp(filepath.Dir(*output), ".payload-")
	if err != nil {
		return err
	}
	defer os.Remove(payload.Name())
	defer payload.Close()
	gz := gzip.NewWriter(payload)
	tw := tar.NewWriter(gz)
	m := osupdate.Manifest{Format: 1, Product: "NanoKVM OS", Kind: "application", Arch: "riscv64", Version: *version, Sequence: *sequence, NativeABI: abi}
	if *system != "" {
		m.Format = 2
		m.Kind = "system"
		base, e := os.ReadFile(*basePath)
		if e != nil {
			return e
		}
		m.SystemBase = strings.TrimSpace(string(base))
	}
	if *removeList != "" {
		raw, e := os.ReadFile(*removeList)
		if e != nil {
			return e
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				m.Remove = append(m.Remove, line)
			}
		}
	}
	for _, name := range names {
		p := filepath.Join(*app, filepath.FromSlash(name))
		if strings.HasPrefix(name, "rootfs/") {
			p = filepath.Join(*system, strings.TrimPrefix(name, "rootfs/"))
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		link := ""
		var sum string
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(p)
			h := sha256.Sum256([]byte(link))
			sum = hex.EncodeToString(h[:])
		} else if info.Mode().IsRegular() {
			sum, err = osupdate.HashFile(p)
		} else {
			return fmt.Errorf("non-regular source")
		}
		if err != nil {
			return err
		}
		mode := uint32(0644)
		if name == "NanoKVM-Server" {
			mode = 0755
		}
		if strings.HasPrefix(name, "rootfs/") && info.Mode()&0111 != 0 && link == "" {
			mode = 0755
		}
		size := info.Size()
		if link != "" {
			size = int64(len(link))
		}
		preserve := strings.HasPrefix(name, "rootfs/etc/") && !strings.HasPrefix(name, "rootfs/etc/init.d/")
		entry := osupdate.Entry{Path: name, Size: size, SHA256: sum, Mode: mode, Link: link, Preserve: preserve}
		m.Files = append(m.Files, entry)
		if err = tw.WriteHeader(&tar.Header{Name: name, Mode: int64(mode), Size: size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		if link != "" {
			if _, err = tw.Write([]byte(link)); err != nil {
				return err
			}
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	if err = payload.Sync(); err != nil {
		return err
	}
	info, err := payload.Stat()
	if err != nil {
		return err
	}
	m.PayloadBytes = info.Size()
	m.PayloadSHA256, err = osupdate.HashFile(payload.Name())
	if err != nil {
		return err
	}
	manifest, err := json.Marshal(m)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(key, append([]byte("NanoKVM OS application update v1\n"), manifest...))
	out, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		out.Close()
		if !success {
			os.Remove(*output)
		}
	}()
	var prefix [12]byte
	copy(prefix[:8], osupdate.Magic)
	binary.BigEndian.PutUint32(prefix[8:], uint32(len(manifest)))
	for _, b := range [][]byte{prefix[:], manifest, sig} {
		if _, err = out.Write(b); err != nil {
			return err
		}
	}
	payload.Seek(0, io.SeekStart)
	if _, err = io.Copy(out, payload); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	verified, err := osupdate.Verify(*output)
	if err != nil {
		return err
	}
	tmp := *output + ".verify"
	if err = osupdate.Extract(*output, verified, tmp); err != nil {
		return err
	}
	os.RemoveAll(tmp)
	success = true
	h := sha256.Sum256(manifest)
	fmt.Printf("Signed %s: version=%s sequence=%d manifest=%s\n", filepath.Base(*output), m.Version, m.Sequence, hex.EncodeToString(h[:]))
	return nil
}
