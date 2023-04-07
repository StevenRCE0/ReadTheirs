package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run main.go <github-repo-link> <branch-name>")
		os.Exit(1)
	}

	repoLink := os.Args[1]
	branchName := "master"

	if len(os.Args) == 3 {
		branchName = os.Args[2]
	}

	u, err := url.Parse(repoLink)
	if err != nil {
		panic(err)
	}

	if !strings.HasPrefix(u.Host, "github.com") {
		fmt.Println("The provided link is not a GitHub repository link")
		os.Exit(1)
	}

	os.MkdirAll(filepath.Join(".", filepath.Base(u.Path)), 0755)

	// retrieve the README.md file from the repository
	readme, err := getReadme(u, branchName)
	if err != nil {
		panic(err)
	}

	// download all assets linked in the README.md file
	err = downloadAssets(readme, u, branchName)
	if err != nil {
		panic(err)
	}

	// generate a bash script to rebase the upstream branch onto the local branch
	os.Chdir(filepath.Join(".", filepath.Base(u.Path)))
	f, err := os.Create("expand.sh")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	_, err = f.WriteString(fmt.Sprintf(`#!/bin/bash
git clone %s .repo
mv -f .repo/* .repo/.* ./
rm -rf .repo
rm expand.sh
git reset --hard
`, repoLink))
	if err != nil {
		panic(err)
	}

	// give it executable permissions
	cmd := exec.Command("chmod", "+x", "expand.sh")
	err = cmd.Run()
	if err != nil {
		panic(err)
	}
}

func getReadme(u *url.URL, b string) (*goquery.Document, error) {
	readmeURL := fmt.Sprintf("%s/raw/%s/README.md", u.String(), b)
	resp, err := http.Get(readmeURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("failed to retrieve the README.md file, status code: %d", resp.StatusCode)
	}

	// create a new buffer and copy the response body into it
	buf := new(bytes.Buffer)
	contentBuffer := new(bytes.Buffer)
	_, err = io.Copy(buf, resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	contentBuffer.Write(buf.Bytes())

	doc, err := goquery.NewDocumentFromReader(buf)
	if err != nil {
		fmt.Printf("failed to parse the README.md file.\n")
		panic(err)
	}
	doc.Find("img[src], link[href], script[src]").Each(func(_ int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			if strings.Contains(src, "?raw=true") {
				s.SetAttr("src", strings.Replace(src, "?raw=true", "", -1))
			}
		}
		if href, exists := s.Attr("href"); exists {
			if strings.Contains(href, "?raw=true") {
				s.SetAttr("href", strings.Replace(href, "?raw=true", "", -1))
			}
		}
	})

	content := contentBuffer.String()

	// remove wrapper from the content string
	content = strings.Replace(content, "?raw=true)", ")", -1)
	aRemoverRegex := regexp.MustCompile(`## <a .*></a>`)
	content = aRemoverRegex.ReplaceAllString(content, "## ")

	// write readme to file
	f, err := os.Create(filepath.Join(".", filepath.Base(u.Path), "README.md"))
	if err != nil {
		return nil, fmt.Errorf("failed to create README.md file: %v", err)
	}
	defer f.Close()
	_, err = io.Copy(f, bytes.NewBufferString(content))
	if err != nil {
		return nil, fmt.Errorf("failed to write README.md file: %v", err)
	}

	return doc, nil
}

func downloadAssets(readme *goquery.Document, u *url.URL, b string) error {
	// find all image, link and script tags that are not local
	assets := []string{}
	// replace all ?raw=true with empty string in readme
	readme.Find("img[src], link[href], script[src]").Each(func(_ int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			if !strings.HasPrefix(src, "http") {
				assets = append(assets, src)
			}
		}
		if href, exists := s.Attr("href"); exists {
			if !strings.HasPrefix(href, "http") {
				assets = append(assets, href)
			}
		}
	})

	// find markdown image links in ()
	regex := regexp.MustCompile(`(\[.*\]\()([^https?://].*\.(png|jpg|gif|svg))\)`)

	content, err := readme.Html()
	if err != nil {
		panic(err)
	}
	content = strings.Replace(content, "?raw=true)", ")", -1)
	matches := regex.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) < 3 || match[2] == "" {
			continue
		}
		assets = append(assets, match[2])
	}

	if len(assets) == 0 {
		return nil
	}

	// create a directory to store the downloaded files
	dir := filepath.Join(".", filepath.Base(u.Path))
	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return err
	}

	// download each asset
	for _, asset := range assets {
		assetURL := fmt.Sprintf("%s/raw/%s/%s", u.String(), b, asset)

		resp, err := http.Get(assetURL)
		if err != nil {
			fmt.Printf("failed to download %s: %v\n", assetURL, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("unexpected status code %d for %s\n", resp.StatusCode, assetURL)
			continue
		}

		// construct the file path to save the downloaded file
		os.MkdirAll(filepath.Join(dir, filepath.Dir(asset)), 0755)
		filePath := filepath.Join(dir, filepath.Dir(asset), filepath.Base(asset))

		// create the file and write the downloaded content to it
		file, err := os.Create(filePath)
		if err != nil {
			fmt.Printf("failed to create file %s: %v\n", filePath, err)
			continue
		}
		defer file.Close()
		_, err = io.Copy(file, resp.Body)
		if err != nil {
			fmt.Printf("failed to write content to file %s: %v\n", filePath, err)
			continue
		}
	}

	return nil
}
