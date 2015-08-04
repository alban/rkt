// Copyright 2015 The rkt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//+build linux

package common

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/coreos/rkt/pkg/keystore"

	"github.com/coreos/rkt/Godeps/_workspace/src/github.com/appc/spec/discovery"
	"github.com/coreos/rkt/Godeps/_workspace/src/golang.org/x/crypto/openpgp"
)

// GetPubKeyLocation either returns the location supplied in argv or discovers one @ prefix
func GetPubKeyLocations(prefix string, args []string, allowHTTP bool, debug bool) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}

	if prefix == "" {
		return nil, fmt.Errorf("at least one key or --prefix required")
	}

	kls, err := metaDiscoverPubKeyLocations(prefix, allowHTTP, debug)
	if err != nil {
		return nil, fmt.Errorf("--prefix meta discovery error: %v", err)
	}

	if len(kls) == 0 {
		return nil, fmt.Errorf("meta discovery on %s resulted in no keys", prefix)
	}

	return kls, nil
}

// metaDiscoverPubKeyLocations discovers the public key through ACDiscovery by applying prefix as an ACApp
func metaDiscoverPubKeyLocations(prefix string, allowHTTP bool, debug bool) ([]string, error) {
	app, err := discovery.NewAppFromString(prefix)
	if err != nil {
		return nil, err
	}

	ep, attempts, err := discovery.DiscoverPublicKeys(*app, allowHTTP)
	if err != nil {
		return nil, err
	}

	if debug {
		for _, a := range attempts {
			fmt.Fprintf(os.Stderr, "meta tag 'ac-discovery-pubkeys' not found on %s: %v\n", a.Prefix, a.Error)
		}
	}

	return ep.Keys, nil
}

// GetPubKey retrieves a public key (if remote), and verifies it's a gpg key
func GetPubKey(location string, allowHTTP bool) (*os.File, error) {
	u, err := url.Parse(location)
	if err != nil {
		return nil, err
	}

	switch u.Scheme {
	case "":
		return os.Open(location)
	case "http":
		if !allowHTTP {
			return nil, fmt.Errorf("--insecure-allow-http required for http URLs")
		}
		fallthrough
	case "https":
		return downloadKey(u.String())
	}

	return nil, fmt.Errorf("only http and https urls supported")
}

// downloadKey retrieves the file, storing it in a deleted tempfile
func downloadKey(url string) (*os.File, error) {
	tf, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, fmt.Errorf("error creating tempfile: %v", err)
	}
	os.Remove(tf.Name()) // no need to keep the tempfile around

	defer func() {
		if err != nil {
			tf.Close()
		}
	}()

	res, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("error getting key: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad HTTP status code: %d", res.StatusCode)
	}

	if _, err := io.Copy(tf, res.Body); err != nil {
		return nil, fmt.Errorf("error copying key: %v", err)
	}

	tf.Seek(0, os.SEEK_SET)

	return tf, nil
}

// AddKeys adds the keys listed in pkls at prefix
func AddKeys(pkls []string, prefix string, allowHTTP bool, forceAccept bool, systemConfigDir, localConfigDir string) error {
	config := keystore.NewConfig(systemConfigDir, localConfigDir)
	ks := keystore.New(config)

	for _, pkl := range pkls {
		pk, err := GetPubKey(pkl, allowHTTP)
		if err != nil {
			return fmt.Errorf("error accessing key: %v", err)
		}
		defer pk.Close()

		accepted, err := reviewKey(prefix, pkl, pk, forceAccept)
		if err != nil {
			return fmt.Errorf("error reviewing key: %v", err)
		}

		if !accepted {
			fmt.Fprintf(os.Stderr, "Not trusting %q\n", pkl)
			continue
		}

		if forceAccept {
			fmt.Fprintf(os.Stderr, "Trusting %q for prefix %q without fingerprint review.\n", pkl, prefix)
		} else {
			fmt.Fprintf(os.Stderr, "Trusting %q for prefix %q after fingerprint review.\n", pkl, prefix)
		}

		if err := addPubKey(prefix, pk, ks); err != nil {
			return fmt.Errorf("Error adding key: %v", err)
		}
	}
	return nil
}

// addPubKey adds a key to the keystore
func addPubKey(prefix string, key *os.File, ks *keystore.Keystore) (err error) {
	var path string
	if prefix == "" {
		path, err = ks.StoreTrustedKeyRoot(key)
		fmt.Fprintf(os.Stderr, "Added root key at %q\n", path)
	} else {
		path, err = ks.StoreTrustedKeyPrefix(prefix, key)
		fmt.Fprintf(os.Stderr, "Added key for prefix %q at %q\n", prefix, path)
	}

	return
}

// reviewKey shows the key summary and conditionally asks the user to accept it
func reviewKey(prefix string, location string, key *os.File, forceAccept bool) (bool, error) {
	defer key.Seek(0, os.SEEK_SET)

	kr, err := openpgp.ReadArmoredKeyRing(key)
	if err != nil {
		return false, fmt.Errorf("error reading key: %v", err)
	}

	fmt.Fprintf(os.Stderr, "Prefix: %q\nKey: %q\n", prefix, location)
	for _, k := range kr {
		fmt.Fprintf(os.Stderr, "GPG key fingerprint is: %s\n", fingerToString(k.PrimaryKey.Fingerprint))
		for _, sk := range k.Subkeys {
			fmt.Fprintf(os.Stderr, "    Subkey fingerprint: %s\n", fingerToString(sk.PublicKey.Fingerprint))
		}
		for n, _ := range k.Identities {
			fmt.Fprintf(os.Stderr, "\t%s\n", n)
		}
	}

	if !forceAccept {
		in := bufio.NewReader(os.Stdin)
		for {
			fmt.Fprintf(os.Stderr, "Are you sure you want to trust this key (yes/no)?\n")
			input, err := in.ReadString('\n')
			if err != nil {
				return false, fmt.Errorf("error reading input: %v", err)
			}
			switch input {
			case "yes\n":
				return true, nil
			case "no\n":
				return false, nil
			default:
				fmt.Fprintf(os.Stderr, "Please enter 'yes' or 'no'\n")
			}
		}
	}
	return true, nil
}

func fingerToString(fpr [20]byte) string {
	str := ""
	for i, b := range fpr {
		if i > 0 && i%2 == 0 {
			str += " "
			if i == 10 {
				str += " "
			}
		}
		str += strings.ToUpper(fmt.Sprintf("%.2x", b))
	}
	return str
}
