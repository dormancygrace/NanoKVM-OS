// Build a complete, source-version-independent signed OS package on the host.
package main

import (
	"NanoKVM-Server/osupdate"
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (err error) {
	images := flag.String("images", "", "directory with rootfs.ext4.gz, normal-boot.sd, ram-update.sd and addons-contract.json")
	meta := flag.String("metadata", "", "full-system metadata JSON generated with the RAM installer")
	keyPath := flag.String("key", "", "release Ed25519 private key (host only)")
	version := flag.String("version", "", "target version")
	sequence := flag.Uint64("sequence", 0, "target monotonic release sequence")
	output := flag.String("output", "", "new .nkos output")
	flag.Parse()
	if *images == "" || *meta == "" || *keyPath == "" || *output == "" {
		return fmt.Errorf("images, metadata, key, version, sequence and output required")
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
		return fmt.Errorf("Ed25519 required")
	}
	pub, err := osupdate.PublicKey()
	if err != nil {
		return err
	}
	if !bytes.Equal(pub, key.Public().(ed25519.PublicKey)) {
		return fmt.Errorf("key does not match device trust key")
	}
	raw, err = os.ReadFile(*meta)
	if err != nil {
		return err
	}
	var full osupdate.FullSystemUpdate
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&full); err != nil {
		return err
	}
	m := osupdate.Manifest{Format: 4, Kind: "full-system", Product: "NanoKVM OS", Arch: "riscv64", Version: *version, Sequence: *sequence, Full: &full}
	payload, err := os.CreateTemp(filepath.Dir(*output), ".full-payload-")
	if err != nil {
		return err
	}
	defer os.Remove(payload.Name())
	defer payload.Close()
	gz := gzip.NewWriter(payload)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"addons-contract.json", "normal-boot.sd", "ram-update.sd", "rootfs.ext4.gz"} {
		path := filepath.Join(*images, name)
		st, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() || st.Size() > osupdate.MaxBundle {
			return fmt.Errorf("invalid image %s", name)
		}
		sum, e := osupdate.HashFile(path)
		if e != nil {
			return e
		}
		m.Files = append(m.Files, osupdate.Entry{Path: name, Size: st.Size(), Mode: 0644, SHA256: sum})
		if e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: st.Size(), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
			return e
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		_, e = io.Copy(tw, f)
		f.Close()
		if e != nil {
			return e
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
	st, err := payload.Stat()
	if err != nil {
		return err
	}
	m.PayloadBytes = st.Size()
	m.PayloadSHA256, err = osupdate.HashFile(payload.Name())
	if err != nil {
		return err
	}
	raw, err = json.Marshal(m)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(key, append([]byte("NanoKVM OS application update v1\n"), raw...))
	out, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(*output)
		}
	}()
	var header [12]byte
	copy(header[:8], osupdate.Magic)
	binary.BigEndian.PutUint32(header[8:], uint32(len(raw)))
	for _, part := range [][]byte{header[:], raw, sig} {
		if _, err = out.Write(part); err != nil {
			return err
		}
	}
	if _, err = payload.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err = io.Copy(out, payload); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	b, err := osupdate.Verify(*output)
	if err != nil {
		return err
	}
	if err = osupdate.VerifyContents(*output, b); err != nil {
		return err
	}
	fmt.Printf("Signed complete system package: %s version=%s sequence=%d\n", *output, *version, *sequence)
	return nil
}
