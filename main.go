package main

// This is a really quick hack to pull some YAML over HTTP and then use it to
// pull some tar files by IPFS CID and extract them.

import (
	"archive/tar"
	"fmt"
	ipfs "github.com/ipfs/go-ipfs-api"
	xz "github.com/ulikunitz/xz"
	"gopkg.in/yaml.v3"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type CloudConfig struct {
	IPFS     IPFSConfig `yaml:"ipfs"`
	Packages []Package  `yaml:"packages"`
	SSHKeys  []string   `yaml:"ssh_keys"`
}

type IPFSConfig struct {
	IPGetCmd string `yaml:"ipget_cmd"`
	IPFSPeer string `yaml:"ipfs_peer"`
}

type Package struct {
	Name string `yaml:"name"`
	CID  string `yaml:"cid"`
	Type string `yaml:"type"`
}

func getCloudInit() (ret string, err error) {
	// Find nocloud=... in /proc/cmdline
	content, err := ioutil.ReadFile("/proc/cmdline")
	log.Printf("cmdline: %s", content)
	if err != nil {
		log.Printf("Failed to read /proc/cmdline: %s", err)
		return
	}

	args := strings.Split(string(content), " ")
	for _, arg := range args {
		if strings.HasPrefix(arg, "nocloud=") {
			// found it
			log.Printf("Found nocloud arg: %s", arg)
			kv := strings.Split(arg, "=")
			ret = strings.Replace(kv[1], "\n", "", -1)
			log.Printf("returning URL: %s", ret)
			return
		}
	}

	log.Printf("Failed to find nocloud arg on kernel command line")
	return
}

func extractPackage(cid string, basedir string, peer string) (err error) {
	// create connection to peer
	i := ipfs.NewShell(peer)

	// Retrieve the object
	obj, err := i.Cat(cid)
	if err != nil {
		return
	}

	// decompress the archive
	x, err := xz.NewReader(obj)
	if err != nil {
		return
	}

	// untar the archive
	tr := tar.NewReader(x)
	for {
		header, e := tr.Next()
		err = e
		if header == nil || err == io.EOF {
			// we're done
			return nil
		}

		if err != nil {
			return
		}

		// extract the specific object
		fn := filepath.Join(basedir, header.Name)
		fmt.Printf("%s\n", fn)
		switch header.Typeflag {
		case tar.TypeDir:
			// create directory
			err = os.MkdirAll(fn, os.FileMode(header.Mode))
			if err != nil {
				return
			}
		case tar.TypeReg:
			// extract file data
			f, e := os.OpenFile(fn, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			err = e
			if err != nil {
				return
			}

			_, err = io.Copy(f, tr)
			if err != nil {
				f.Close()
				return
			}
			f.Close()
		}
	}
}

func main() {
	var c CloudConfig

	url, err := getCloudInit()
	if err != nil || url == "" {
		log.Fatal(fmt.Printf("No URL provided, bailing: %s", err))
	}

	resp, err := http.Get(url)
	if err != nil {
		log.Fatal(fmt.Printf("Failed to retrieve URL: %s", err))
	}
	defer resp.Body.Close()

	fmt.Println("Response status:", resp.Status)
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(fmt.Printf("Failed to parse response body: %s", err))
	}

	// Unmarshal the YAML
	err = yaml.Unmarshal(body, &c)
	if err != nil {
		log.Fatal(fmt.Printf("Failed to parse YAML content: %s", err))
	}

	for i, pkg := range c.Packages {
		log.Printf("Deploying package %d: %s: cid=%s", i, pkg.Name, pkg.CID)
		// extract package (tar.xz). TODO: path prefix is hard-coded...
		err = extractPackage(pkg.CID, "/", c.IPFS.IPFSPeer)
		if err != nil {
			log.Printf("Failed to extract package %s: %s", pkg.Name, err)
		}

		if pkg.Type == "service" {
			// Use systemd to enable it. Best effort at best.
			err = exec.Command("/bin/systemctl", "enable", pkg.Name).Run()
			if err != nil {
				log.Printf("Failed to enable %s: %s", pkg.Name, err)
			}
			err = exec.Command("/bin/systemctl", "start", pkg.Name).Run()
			if err != nil {
				log.Printf("Failed to enable %s: %s", pkg.Name, err)
			}
		}

	}
}
