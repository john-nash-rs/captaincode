package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// releasesURL is the public release list. It is a fixed, unauthenticated
// endpoint read only to tell someone running an old build that they are.
const releasesURL = "https://api.github.com/repos/john-nash-rs/captaincode/releases/latest"

// newestRelease returns the most recent published version, or an empty string.
//
// A failure here is never fatal and never reported as one: doctor is a
// diagnostic, and a check that needs the network must not turn an offline
// machine into a failing run.
func newestRelease() (string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(releasesURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("release lookup failed")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		return "", err
	}
	var release struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", err
	}
	if release.Draft || release.TagName == "" {
		return "", nil
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}
