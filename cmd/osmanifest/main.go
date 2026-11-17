// Command osmanifest builds and signs a FELDCORE OS update manifest.
// The signing key is never written into an image and must not be committed.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Herculade0/feldcore/internal/osupdate"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "genkey":
		genkey(os.Args[2:])
	case "pub":
		pub(os.Args[2:])
	case "manifest":
		manifest(os.Args[2:])
	case "sign":
		sign(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `osmanifest builds a signed FELDCORE OS release description.

  osmanifest genkey -pub update.pub -key update.key
  osmanifest pub -key update.key -pub update.pub
  osmanifest manifest -dir dist/os -version 0.2.0 -notes-file NOTES -out dist/os/feldcore-os.json
  osmanifest sign -key update.key -in dist/os/feldcore-os.json

The key file is the unencrypted signing secret. Keep it out of the repository.
`)
}

func genkey(args []string) {
	fs := flag.NewFlagSet("genkey", flag.ExitOnError)
	pub := fs.String("pub", "", "public key file to write")
	key := fs.String("key", "", "private key file to write")
	_ = fs.Parse(args)
	if *pub == "" || *key == "" {
		fmt.Fprintln(os.Stderr, "-pub and -key are required")
		os.Exit(2)
	}
	k, err := osupdate.GenerateKey()
	if err != nil {
		die(err)
	}
	if err := writeKey(*key, []byte(osupdate.MarshalPrivate(k))); err != nil {
		die(err)
	}
	if err := os.WriteFile(*pub, []byte(osupdate.MarshalPublic(k.Public())), 0o644); err != nil {
		die(err)
	}
	fmt.Println(k.Public().ID())
}

func pub(args []string) {
	fs := flag.NewFlagSet("pub", flag.ExitOnError)
	key := fs.String("key", "", "private key file")
	out := fs.String("pub", "", "public key file to write")
	_ = fs.Parse(args)
	if *key == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "-key and -pub are required")
		os.Exit(2)
	}
	secret, err := os.ReadFile(*key)
	if err != nil {
		die(err)
	}
	k, err := osupdate.ParsePrivate(string(secret))
	if err != nil {
		die(err)
	}
	if err := os.WriteFile(*out, []byte(osupdate.MarshalPublic(k.Public())), 0o644); err != nil {
		die(err)
	}
	fmt.Println(k.Public().ID())
}

func manifest(args []string) {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	dir := fs.String("dir", "", "directory of root, kernel, initrd, and optional sysexts")
	version := fs.String("version", "", "release version")
	notesFile := fs.String("notes-file", "", "release notes file")
	kernel := fs.String("kernel", "", "kernel version this image boots")
	channel := fs.String("channel", "stable", "release channel")
	out := fs.String("out", "", "manifest path")
	_ = fs.Parse(args)
	if *dir == "" || *version == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "-dir, -version, and -out are required")
		os.Exit(2)
	}
	notes := ""
	if *notesFile != "" {
		b, err := os.ReadFile(*notesFile)
		if err != nil {
			die(err)
		}
		notes = string(b)
	}
	m, err := osupdate.BuildManifest(*dir, osupdate.Release{
		Version: *version, Channel: *channel, Notes: notes, Arch: "amd64", Kernel: *kernel,
	})
	if err != nil {
		die(err)
	}
	raw, err := osupdate.MarshalManifest(m)
	if err != nil {
		die(err)
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		die(err)
	}
}

func sign(args []string) {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	key := fs.String("key", "", "private key file")
	in := fs.String("in", "", "file to sign")
	_ = fs.Parse(args)
	if *key == "" || *in == "" {
		fmt.Fprintln(os.Stderr, "-key and -in are required")
		os.Exit(2)
	}
	secret, err := os.ReadFile(*key)
	if err != nil {
		die(err)
	}
	k, err := osupdate.ParsePrivate(string(secret))
	if err != nil {
		die(err)
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		die(err)
	}
	sig, err := osupdate.Sign(k, filepath.Base(*in), data)
	if err != nil {
		die(err)
	}
	if err := os.WriteFile(*in+".minisig", sig, 0o644); err != nil {
		die(err)
	}
}

func writeKey(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Close()
}

func die(err error) {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(err.Error()))
	os.Exit(1)
}
