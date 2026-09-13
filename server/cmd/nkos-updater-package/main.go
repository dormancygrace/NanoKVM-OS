// Build a signed, independently installable updater package on the host.
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
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	binaryPath := flag.String("binary", "", "static riscv64 nkos-update binary")
	keyPath := flag.String("key", "", "release Ed25519 private key (host only)")
	version := flag.String("version", "", "updater version")
	sequence := flag.Uint64("sequence", 0, "updater monotonic sequence")
	capability := flag.Uint("capability", 0, "target updater capability")
	minimum := flag.Uint("minimum-source", uint(osupdate.UpdaterCapability), "minimum installed updater capability")
	output := flag.String("output", "", "new .nkos output")
	flag.Parse()
	if *binaryPath == "" || *keyPath == "" || *version == "" || *sequence == 0 || *capability == 0 || *output == "" {
		return fmt.Errorf("binary, key, version, sequence, capability and output required")
	}
	if err = osupdate.ValidateStaticUpdater(*binaryPath); err != nil {
		return err
	}
	rawKey, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(rawKey)
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
	trusted, err := osupdate.PublicKey()
	if err != nil || !bytes.Equal(trusted, key.Public().(ed25519.PublicKey)) {
		return fmt.Errorf("key does not match device trust key")
	}
	info, err := os.Lstat(*binaryPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return fmt.Errorf("invalid updater binary")
	}
	sum, err := osupdate.HashFile(*binaryPath)
	if err != nil {
		return err
	}
	payload, err := os.CreateTemp(filepath.Dir(*output), ".updater-payload-")
	if err != nil {
		return err
	}
	defer os.Remove(payload.Name())
	defer payload.Close()
	gz, err := gzip.NewWriterLevel(payload, gzip.BestCompression)
	if err != nil {
		return err
	}
	gz.Header.ModTime = time.Unix(0, 0)
	gz.Header.OS = 255
	tw := tar.NewWriter(gz)
	header := &tar.Header{Name: "nkos-update", Mode: 0755, Size: info.Size(), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, ModTime: time.Unix(0, 0)}
	if err = tw.WriteHeader(header); err != nil {
		return err
	}
	in, err := os.Open(*binaryPath)
	if err != nil {
		return err
	}
	_, err = io.Copy(tw, in)
	closeErr := in.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
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
	stat, err := payload.Stat()
	if err != nil {
		return err
	}
	m := osupdate.Manifest{
		Format: 5, Kind: "updater", Product: "NanoKVM OS", Arch: "riscv64", Version: *version, Sequence: *sequence,
		Updater: &osupdate.UpdaterUpdate{Capability: uint32(*capability), MinimumSource: uint32(*minimum)},
		Files:   []osupdate.Entry{{Path: "nkos-update", Size: info.Size(), Mode: 0755, SHA256: sum}}, PayloadBytes: stat.Size(),
	}
	m.PayloadSHA256, err = osupdate.HashFile(payload.Name())
	if err != nil {
		return err
	}
	raw, err := json.Marshal(m)
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
	var prefix [12]byte
	copy(prefix[:8], osupdate.Magic)
	binary.BigEndian.PutUint32(prefix[8:], uint32(len(raw)))
	for _, part := range [][]byte{prefix[:], raw, sig} {
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
	bundle, err := osupdate.Verify(*output)
	if err == nil {
		err = osupdate.VerifyContents(*output, bundle)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Signed updater package: %s capability=%d sequence=%d\n", *output, *capability, *sequence)
	return nil
}
