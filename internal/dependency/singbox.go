package dependency

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Result struct {
	Current, Latest string
	Updated         bool
}

type release struct {
	TagName    string         `json:"tag_name"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func UpdateSingBox(ctx context.Context, client *http.Client, binaryPath string) (Result, error) {
	current, err := binaryVersion(ctx, binaryPath)
	if err != nil {
		return Result{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/SagerNet/sing-box/releases/latest", nil)
	req.Header.Set("User-Agent", "ProxyLens dependency updater")
	response, err := client.Do(req)
	if err != nil {
		return Result{Current: current}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Result{Current: current}, fmt.Errorf("GitHub release API HTTP %d", response.StatusCode)
	}
	var latest release
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&latest); err != nil {
		return Result{Current: current}, err
	}
	latestVersion := strings.TrimPrefix(latest.TagName, "v")
	result := Result{Current: current, Latest: latestVersion}
	if latest.Prerelease || compareVersion(latestVersion, current) <= 0 {
		return result, nil
	}
	ext := ".tar.gz"
	variant := ""
	if runtime.GOOS == "windows" {
		ext = ".zip"
	} else if runtime.GOOS == "linux" {
		variant = linuxLibcVariant()
	}
	name := fmt.Sprintf("sing-box-%s-%s-%s%s%s", latestVersion, runtime.GOOS, runtime.GOARCH, variant, ext)
	var asset *releaseAsset
	for i := range latest.Assets {
		if latest.Assets[i].Name == name {
			asset = &latest.Assets[i]
			break
		}
	}
	if asset == nil {
		return result, fmt.Errorf("official release has no asset %s", name)
	}
	if asset.Size <= 0 || asset.Size > 96<<20 || !strings.HasPrefix(asset.Digest, "sha256:") {
		return result, errors.New("release asset lacks a usable size or SHA-256 digest")
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	req.Header.Set("User-Agent", "ProxyLens dependency updater")
	response, err = client.Do(req)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("sing-box asset HTTP %d", response.StatusCode)
	}
	archiveFile, err := os.CreateTemp("", "proxylens-sing-box-*"+ext)
	if err != nil {
		return result, err
	}
	archivePath := archiveFile.Name()
	defer os.Remove(archivePath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(archiveFile, hash), io.LimitReader(response.Body, asset.Size+1))
	closeErr := archiveFile.Close()
	if copyErr != nil || closeErr != nil || written != asset.Size {
		return result, fmt.Errorf("incomplete sing-box asset: got %d, expected %d: copy=%v close=%v", written, asset.Size, copyErr, closeErr)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), strings.TrimPrefix(asset.Digest, "sha256:")) {
		return result, errors.New("sing-box asset SHA-256 mismatch")
	}
	tmp := binaryPath + ".update"
	if err = extractBinary(archivePath, ext, tmp); err != nil {
		return result, err
	}
	defer os.Remove(tmp)
	if err = os.Chmod(tmp, 0755); err != nil {
		return result, err
	}
	if version, versionErr := binaryVersion(ctx, tmp); versionErr != nil || version != latestVersion {
		return result, fmt.Errorf("downloaded sing-box failed version check: version=%s error=%v", version, versionErr)
	}
	config := []byte(`{"inbounds":[{"type":"mixed","tag":"in","listen":"127.0.0.1","listen_port":19999}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct","auto_detect_interface":true}}`)
	configPath := binaryPath + ".update-check.json"
	if err = os.WriteFile(configPath, config, 0600); err != nil {
		return result, err
	}
	defer os.Remove(configPath)
	if output, checkErr := exec.CommandContext(ctx, tmp, "check", "-c", configPath).CombinedOutput(); checkErr != nil {
		return result, fmt.Errorf("new sing-box rejected smoke config: %w: %s", checkErr, strings.TrimSpace(string(output)))
	}
	previous := binaryPath + ".previous"
	_ = os.Remove(previous)
	if err = os.Rename(binaryPath, previous); err != nil {
		return result, err
	}
	if err = os.Rename(tmp, binaryPath); err != nil {
		_ = os.Rename(previous, binaryPath)
		return result, err
	}
	_ = os.Remove(previous)
	result.Updated = true
	return result, nil
}

func linuxLibcVariant() string {
	// OpenWrt and Alpine use musl. Starting with sing-box 1.14, the official
	// generic Linux archive can require a loader that is absent on these
	// systems; exec then misleadingly reports "no such file or directory".
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		return "-musl"
	}
	if raw, err := os.ReadFile("/etc/os-release"); err == nil {
		lower := strings.ToLower(string(raw))
		if strings.Contains(lower, "id=alpine") || strings.Contains(lower, "id=openwrt") {
			return "-musl"
		}
	}
	return "-glibc"
}

func binaryVersion(ctx context.Context, path string) (string, error) {
	output, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(output))
	for i := range fields {
		if fields[i] == "version" && i+1 < len(fields) {
			return strings.TrimPrefix(fields[i+1], "v"), nil
		}
	}
	return "", errors.New("cannot parse sing-box version")
}

// Version reports the installed sing-box core version.
func Version(ctx context.Context, path string) (string, error) {
	return binaryVersion(ctx, path)
}

func compareVersion(a, b string) int {
	parse := func(value string) [3]int {
		var out [3]int
		for i, part := range strings.SplitN(strings.SplitN(value, "-", 2)[0], ".", 3) {
			out[i], _ = strconv.Atoi(part)
		}
		return out
	}
	av, bv := parse(a), parse(b)
	for i := range av {
		if av[i] < bv[i] {
			return -1
		}
		if av[i] > bv[i] {
			return 1
		}
	}
	return 0
}

func extractBinary(archivePath, ext, outputPath string) error {
	write := func(reader io.Reader) error {
		output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(reader, 128<<20))
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	if ext == ".zip" {
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if strings.EqualFold(filepath.Base(file.Name), "sing-box.exe") {
				body, openErr := file.Open()
				if openErr != nil {
					return openErr
				}
				defer body.Close()
				return write(body)
			}
		}
	} else {
		archiveFile, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer archiveFile.Close()
		gz, err := gzip.NewReader(archiveFile)
		if err != nil {
			return err
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			header, nextErr := reader.Next()
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if nextErr != nil {
				return nextErr
			}
			if filepath.Base(header.Name) == "sing-box" && header.Size > 0 && header.Size <= 128<<20 {
				return write(io.LimitReader(reader, header.Size))
			}
		}
	}
	return errors.New("sing-box executable missing from official archive")
}
