package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	siteBase  = "https://mods.vintagestory.at"
	apiBase   = siteBase + "/api"
	userAgent = "vsmm-go/0.1 (+https://mods.vintagestory.at)"
)

var (
	showPageIdentifierRE = regexp.MustCompile(`vintagestorymodinstall://([A-Za-z0-9_.-]+)@`)
	defaultCfgPath       = defaultConfigPath()
)

type Config struct {
	ModDir      string     `json:"mod_dir"`
	CacheDir    string     `json:"cache_dir"`
	BackupDir   string     `json:"backup_dir"`
	GameVersion string     `json:"game_version"`
	Mods        []ModEntry `json:"mods"`
}

type ModEntry struct {
	ID            string `json:"id"`
	ModDBID       int    `json:"moddb_id,omitempty"`
	AssetID       int    `json:"asset_id,omitempty"`
	Name          string `json:"name,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	Strategy      string `json:"strategy"`
	PinnedVersion string `json:"pinned_version,omitempty"`
	GameVersion   string `json:"game_version,omitempty"`
}

type PathSet struct {
	ModDir    string
	CacheDir  string
	BackupDir string
	StateFile string
}

type SearchResponse struct {
	StatusCode string      `json:"statuscode"`
	Mods       []SearchMod `json:"mods"`
}

type SearchMod struct {
	ModID     int      `json:"modid"`
	AssetID   int      `json:"assetid"`
	Downloads int      `json:"downloads"`
	Name      string   `json:"name"`
	Author    string   `json:"author"`
	ModIDStrs []string `json:"modidstrs"`
}

type ModResponse struct {
	StatusCode string  `json:"statuscode"`
	Mod        *APIMod `json:"mod"`
}

type APIMod struct {
	ModID     int          `json:"modid"`
	AssetID   int          `json:"assetid"`
	Name      string       `json:"name"`
	Author    string       `json:"author"`
	Downloads int          `json:"downloads"`
	URLAlias  *string      `json:"urlalias"`
	Releases  []APIRelease `json:"releases"`
}

type APIRelease struct {
	MainFile   string   `json:"mainfile"`
	Filename   string   `json:"filename"`
	ModIDStr   string   `json:"modidstr"`
	ModVersion string   `json:"modversion"`
	Created    string   `json:"created"`
	Tags       []string `json:"tags"`
}

type SyncState struct {
	Installed   map[string]InstalledRecord `json:"installed"`
	LastSyncUTC string                     `json:"last_sync_utc"`
}

type InstalledRecord struct {
	Filename  string `json:"filename"`
	Version   string `json:"version"`
	ModDBID   int    `json:"moddb_id"`
	AssetID   int    `json:"asset_id"`
	SyncedUTC string `json:"synced_utc"`
}

type SyncResult struct {
	Downloaded int
	Skipped    int
	Removed    int
	Failed     int
	BackupFile string
	Logs       []string
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printMainUsage()
		return 1
	}

	switch args[0] {
	case "init":
		return cmdInit(args[1:])
	case "search":
		return cmdSearch(args[1:])
	case "show":
		return cmdShow(args[1:])
	case "add":
		return cmdAdd(args[1:])
	case "browse":
		return cmdBrowse(args[1:])
	case "list":
		return cmdList(args[1:])
	case "remove":
		return cmdRemove(args[1:])
	case "sync", "install":
		return cmdSync(args[1:])
	case "config":
		return cmdConfig(args[1:])
	case "backup-create":
		return cmdBackupCreate(args[1:])
	case "backup-list":
		return cmdBackupList(args[1:])
	case "backup-restore":
		return cmdBackupRestore(args[1:])
	case "-h", "--help", "help":
		printMainUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		printMainUsage()
		return 1
	}
}

func printMainUsage() {
	fmt.Println("vsmm: Vintage Story Mod Manager (Go)")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  vsmm <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  init            Create config file")
	fmt.Println("  search          Search mods")
	fmt.Println("  show            Show mod details and releases")
	fmt.Println("  add             Add/update mod in config")
	fmt.Println("  browse          Interactive search + add")
	fmt.Println("  list            List configured mods")
	fmt.Println("  remove          Remove mod from config")
	fmt.Println("  sync            Download/sync configured mods")
	fmt.Println("  install         Alias of sync")
	fmt.Println("  config          View/update config values")
	fmt.Println("  backup-create   Create zip backup")
	fmt.Println("  backup-list     List backups")
	fmt.Println("  backup-restore  Restore a backup")
	fmt.Println()
	fmt.Printf("Default config path: %s\n", defaultCfgPath)
}

func newConfig(modDir string, gameVersion string) Config {
	if strings.TrimSpace(modDir) == "" {
		modDir = detectDefaultModDir()
	}
	return Config{
		ModDir:      modDir,
		CacheDir:    filepath.Join(defaultVSMMHome(), "cache"),
		BackupDir:   filepath.Join(defaultVSMMHome(), "backups"),
		GameVersion: strings.TrimSpace(gameVersion),
		Mods:        []ModEntry{},
	}
}

func normalizeConfig(cfg *Config) {
	if strings.TrimSpace(cfg.ModDir) == "" {
		cfg.ModDir = detectDefaultModDir()
	}
	if strings.TrimSpace(cfg.CacheDir) == "" {
		cfg.CacheDir = filepath.Join(defaultVSMMHome(), "cache")
	}
	if strings.TrimSpace(cfg.BackupDir) == "" {
		cfg.BackupDir = filepath.Join(defaultVSMMHome(), "backups")
	}
	if cfg.Mods == nil {
		cfg.Mods = []ModEntry{}
	}
}

func defaultVSMMHome() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".vsmm")
	}
	return ".vsmm"
}

func defaultConfigPath() string {
	return filepath.Join(defaultVSMMHome(), "config.json")
}

func detectDefaultModDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		if appData := os.Getenv("APPDATA"); strings.TrimSpace(appData) != "" {
			return filepath.Join(appData, "VintagestoryData", "Mods")
		}
		if strings.TrimSpace(home) != "" {
			return filepath.Join(home, "AppData", "Roaming", "VintagestoryData", "Mods")
		}
	case "darwin":
		if strings.TrimSpace(home) != "" {
			return filepath.Join(home, "Library", "Application Support", "VintagestoryData", "Mods")
		}
	default:
		if strings.TrimSpace(home) != "" {
			return filepath.Join(home, ".config", "VintagestoryData", "Mods")
		}
	}
	return "VintagestoryData/Mods"
}

func expandUserPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func absConfigPath(raw string) string {
	p := expandUserPath(strings.TrimSpace(raw))
	if p == "" {
		p = defaultCfgPath
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func resolvePath(pathValue string, cfgPath string) string {
	pathValue = expandUserPath(strings.TrimSpace(pathValue))
	if filepath.IsAbs(pathValue) {
		return filepath.Clean(pathValue)
	}
	base := filepath.Dir(cfgPath)
	return filepath.Clean(filepath.Join(base, pathValue))
}

func getPaths(cfg *Config, cfgPath string) PathSet {
	modDir := resolvePath(cfg.ModDir, cfgPath)
	cacheDir := resolvePath(cfg.CacheDir, cfgPath)
	backupDir := resolvePath(cfg.BackupDir, cfgPath)
	return PathSet{
		ModDir:    modDir,
		CacheDir:  cacheDir,
		BackupDir: backupDir,
		StateFile: filepath.Join(cacheDir, "state.json"),
	}
}

func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	normalizeConfig(&cfg)
	return &cfg, nil
}

func printLoadConfigError(err error, cfgPath string) {
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "config not found: %s\n", cfgPath)
		fmt.Fprintf(os.Stderr, "run: vsmm init --config %s\n", cfgPath)
		return
	}
	fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
}

func saveConfig(path string, cfg *Config) error {
	normalizeConfig(cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func findModIndex(cfg *Config, modID string) int {
	want := strings.ToLower(strings.TrimSpace(modID))
	for i := range cfg.Mods {
		if strings.ToLower(strings.TrimSpace(cfg.Mods[i].ID)) == want {
			return i
		}
	}
	return -1
}

func upsertMod(cfg *Config, entry ModEntry) {
	idx := findModIndex(cfg, entry.ID)
	if idx == -1 {
		cfg.Mods = append(cfg.Mods, entry)
		return
	}
	cfg.Mods[idx] = entry
}

func removeMod(cfg *Config, modID string) bool {
	idx := findModIndex(cfg, modID)
	if idx == -1 {
		return false
	}
	cfg.Mods = append(cfg.Mods[:idx], cfg.Mods[idx+1:]...)
	return true
}

func fetchJSON(rawURL string, out any) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)

	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("http %d from %s: %s", res.StatusCode, rawURL, strings.TrimSpace(string(body)))
	}

	return json.NewDecoder(res.Body).Decode(out)
}

func fetchText(rawURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	res, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return "", fmt.Errorf("http %d from %s", res.StatusCode, rawURL)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func searchMods(query string, limit int, orderBy string) ([]SearchMod, error) {
	v := url.Values{}
	v.Set("text", query)
	v.Set("orderby", orderBy)
	v.Set("orderdirection", "desc")
	u := apiBase + "/mods?" + v.Encode()

	var resp SearchResponse
	if err := fetchJSON(u, &resp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(resp.StatusCode) != "200" {
		return nil, fmt.Errorf("search failed (statuscode=%s)", strings.TrimSpace(resp.StatusCode))
	}
	if limit <= 0 || limit > len(resp.Mods) {
		limit = len(resp.Mods)
	}
	return resp.Mods[:limit], nil
}

func tryGetMod(ref string) (*APIMod, error) {
	escaped := url.PathEscape(ref)
	u := apiBase + "/mod/" + escaped
	var resp ModResponse
	if err := fetchJSON(u, &resp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(resp.StatusCode) == "200" && resp.Mod != nil {
		return resp.Mod, nil
	}
	return nil, nil
}

func identifierFromShowPage(showURL string) string {
	html, err := fetchText(showURL)
	if err != nil {
		return ""
	}
	m := showPageIdentifierRE.FindStringSubmatch(html)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func candidateRefs(rawRef string) []string {
	rawRef = strings.TrimSpace(rawRef)
	if rawRef == "" {
		return nil
	}
	candidates := []string{rawRef}
	if strings.HasPrefix(rawRef, "http://") || strings.HasPrefix(rawRef, "https://") {
		if parsed, err := url.Parse(rawRef); err == nil {
			path := strings.Trim(parsed.Path, "/")
			if path != "" {
				parts := strings.Split(path, "/")
				if len(parts) >= 3 && parts[0] == "show" && parts[1] == "mod" {
					assetID := parts[2]
					candidates = append(candidates, assetID)
					if ident := identifierFromShowPage(siteBase + "/show/mod/" + assetID); ident != "" {
						candidates = append(candidates, ident)
					}
				} else if len(parts) >= 1 {
					candidates = append(candidates, parts[0])
				}
			}
		}
	} else if strings.HasPrefix(rawRef, "show/mod/") {
		assetID := strings.TrimPrefix(rawRef, "show/mod/")
		candidates = append(candidates, assetID)
		if ident := identifierFromShowPage(siteBase + "/show/mod/" + assetID); ident != "" {
			candidates = append(candidates, ident)
		}
	} else if isDigits(rawRef) {
		if ident := identifierFromShowPage(siteBase + "/show/mod/" + rawRef); ident != "" {
			candidates = append(candidates, ident)
		}
	}

	out := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

func getMod(ref string) (*APIMod, error) {
	for _, candidate := range candidateRefs(ref) {
		mod, err := tryGetMod(candidate)
		if err != nil {
			return nil, err
		}
		if mod != nil {
			return mod, nil
		}
	}

	mods, err := searchMods(ref, 15, "lastreleased")
	if err != nil {
		return nil, err
	}
	refLower := strings.ToLower(strings.TrimSpace(ref))

	for _, item := range mods {
		for _, ident := range item.ModIDStrs {
			if strings.ToLower(strings.TrimSpace(ident)) == refLower {
				return getMod(ident)
			}
		}
	}
	for _, item := range mods {
		if strings.ToLower(strings.TrimSpace(item.Name)) == refLower {
			return getMod(strconv.Itoa(item.ModID))
		}
	}
	if len(mods) > 0 {
		return getMod(strconv.Itoa(mods[0].ModID))
	}

	return nil, fmt.Errorf("could not resolve mod reference: %s", ref)
}

func primaryIdentifier(mod *APIMod) string {
	for _, r := range mod.Releases {
		if strings.TrimSpace(r.ModIDStr) != "" {
			return strings.TrimSpace(r.ModIDStr)
		}
	}
	if mod.ModID != 0 {
		return strconv.Itoa(mod.ModID)
	}
	return ""
}

func modHomeURL(mod *APIMod) string {
	if mod.URLAlias != nil && strings.TrimSpace(*mod.URLAlias) != "" {
		return siteBase + "/" + strings.TrimSpace(*mod.URLAlias)
	}
	if mod.AssetID != 0 {
		return fmt.Sprintf("%s/show/mod/%d", siteBase, mod.AssetID)
	}
	return siteBase
}

func releaseMatchesGameVersion(release APIRelease, gameVersion string) bool {
	target := strings.ToLower(strings.TrimSpace(gameVersion))
	if target == "" {
		return true
	}
	prefix := target
	parts := strings.Split(target, ".")
	if len(parts) >= 2 {
		prefix = parts[0] + "." + parts[1]
	}
	for _, tag := range release.Tags {
		t := strings.ToLower(strings.TrimSpace(tag))
		if t == "" {
			continue
		}
		if t == target {
			return true
		}
		if t == prefix || strings.HasPrefix(t, prefix+".") || strings.HasPrefix(t, prefix+"-") {
			return true
		}
		if strings.Contains(t, target) {
			return true
		}
	}
	return false
}

func sortedReleases(mod *APIMod) []APIRelease {
	releases := append([]APIRelease(nil), mod.Releases...)
	sort.SliceStable(releases, func(i, j int) bool {
		if releases[i].Created == releases[j].Created {
			return releases[i].ModVersion > releases[j].ModVersion
		}
		return releases[i].Created > releases[j].Created
	})
	return releases
}

func pickRelease(mod *APIMod, desiredVersion string, gameVersion string) (*APIRelease, error) {
	releases := sortedReleases(mod)
	if len(releases) == 0 {
		return nil, errors.New("mod has no releases")
	}

	if strings.TrimSpace(desiredVersion) != "" {
		want := strings.ToLower(strings.TrimSpace(desiredVersion))
		for i := range releases {
			if strings.ToLower(strings.TrimSpace(releases[i].ModVersion)) == want {
				return &releases[i], nil
			}
		}
		return nil, fmt.Errorf("release version %s not found", desiredVersion)
	}

	if strings.TrimSpace(gameVersion) != "" {
		for i := range releases {
			if releaseMatchesGameVersion(releases[i], gameVersion) {
				return &releases[i], nil
			}
		}
	}

	return &releases[0], nil
}

func releaseFilename(release *APIRelease) string {
	if strings.TrimSpace(release.Filename) != "" {
		return strings.TrimSpace(release.Filename)
	}
	mainfile := strings.TrimSpace(release.MainFile)
	if mainfile == "" {
		return ""
	}
	if parsed, err := url.Parse(mainfile); err == nil {
		if dl := parsed.Query().Get("dl"); strings.TrimSpace(dl) != "" {
			return dl
		}
		return filepath.Base(parsed.Path)
	}
	return filepath.Base(mainfile)
}

func fileExistsNonZero(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func downloadFile(rawURL string, destination string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("empty download url")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)

	downloadClient := &http.Client{Timeout: 120 * time.Second}
	res, err := downloadClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("download failed http %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}

	partFile := destination + ".part"
	out, err := os.Create(partFile)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, res.Body); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(partFile, destination)
}

func copyFile(src string, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func loadState(path string) (*SyncState, error) {
	state := &SyncState{Installed: map[string]InstalledRecord{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}
	if state.Installed == nil {
		state.Installed = map[string]InstalledRecord{}
	}
	return state, nil
}

func saveState(path string, state *SyncState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func syncMods(cfg *Config, paths PathSet, dryRun bool, prune bool, backupFirst bool) (SyncResult, error) {
	result := SyncResult{Logs: []string{}}

	if err := os.MkdirAll(paths.ModDir, 0o755); err != nil {
		return result, err
	}
	if err := os.MkdirAll(paths.CacheDir, 0o755); err != nil {
		return result, err
	}

	state, err := loadState(paths.StateFile)
	if err != nil {
		return result, err
	}

	if backupFirst && !dryRun {
		backupFile, err := createBackup(paths.ModDir, paths.BackupDir, "")
		if err != nil {
			return result, err
		}
		result.BackupFile = backupFile
		result.Logs = append(result.Logs, "Backup created: "+backupFile)
	}

	desiredIDs := map[string]struct{}{}
	globalGV := strings.TrimSpace(cfg.GameVersion)

	for _, entry := range cfg.Mods {
		ref := strings.TrimSpace(entry.ID)
		if ref == "" && entry.ModDBID != 0 {
			ref = strconv.Itoa(entry.ModDBID)
		}
		if ref == "" {
			result.Failed++
			result.Logs = append(result.Logs, "[err] skipping entry missing id and moddb_id")
			continue
		}

		mod, err := getMod(ref)
		if err != nil {
			result.Failed++
			result.Logs = append(result.Logs, fmt.Sprintf("[err] %s: %v", ref, err))
			continue
		}

		modID := strings.TrimSpace(entry.ID)
		if modID == "" {
			modID = primaryIdentifier(mod)
		}
		if modID == "" {
			result.Failed++
			result.Logs = append(result.Logs, fmt.Sprintf("[err] %s: could not resolve identifier", ref))
			continue
		}
		desiredIDs[modID] = struct{}{}

		strategy := strings.ToLower(strings.TrimSpace(entry.Strategy))
		if strategy == "" {
			strategy = "latest"
		}
		desiredVersion := ""
		if strategy == "pinned" {
			desiredVersion = strings.TrimSpace(entry.PinnedVersion)
		}

		entryGV := strings.TrimSpace(entry.GameVersion)
		if entryGV == "" {
			entryGV = globalGV
		}

		release, err := pickRelease(mod, desiredVersion, entryGV)
		if err != nil {
			result.Failed++
			result.Logs = append(result.Logs, fmt.Sprintf("[err] %s: %v", modID, err))
			continue
		}
		filename := releaseFilename(release)
		if filename == "" {
			result.Failed++
			result.Logs = append(result.Logs, fmt.Sprintf("[err] %s: release has no filename", modID))
			continue
		}

		target := filepath.Join(paths.ModDir, filename)
		cacheFile := filepath.Join(paths.CacheDir, filename)
		present := fileExistsNonZero(target)
		if present {
			result.Skipped++
			result.Logs = append(result.Logs, fmt.Sprintf("[skip] %s %s already present (%s)", modID, release.ModVersion, filename))
		} else {
			if dryRun {
				result.Downloaded++
				result.Logs = append(result.Logs, fmt.Sprintf("[dry-run] would download %s %s -> %s", modID, release.ModVersion, filename))
			} else {
				if !fileExistsNonZero(cacheFile) {
					if err := downloadFile(release.MainFile, cacheFile); err != nil {
						result.Failed++
						result.Logs = append(result.Logs, fmt.Sprintf("[err] %s: %v", modID, err))
						continue
					}
				}
				if err := copyFile(cacheFile, target); err != nil {
					result.Failed++
					result.Logs = append(result.Logs, fmt.Sprintf("[err] %s: %v", modID, err))
					continue
				}
				result.Downloaded++
				result.Logs = append(result.Logs, fmt.Sprintf("[down] %s %s -> %s", modID, release.ModVersion, filename))
			}
		}

		prev, ok := state.Installed[modID]
		if ok && prev.Filename != "" && prev.Filename != filename {
			stale := filepath.Join(paths.ModDir, prev.Filename)
			if fileExistsNonZero(stale) {
				if dryRun {
					result.Logs = append(result.Logs, fmt.Sprintf("[dry-run] would remove stale file %s", prev.Filename))
				} else {
					if err := os.Remove(stale); err != nil {
						result.Failed++
						result.Logs = append(result.Logs, fmt.Sprintf("[err] %s: failed removing stale file %s: %v", modID, prev.Filename, err))
					} else {
						result.Removed++
						result.Logs = append(result.Logs, fmt.Sprintf("[rm] removed stale file %s", prev.Filename))
					}
				}
			}
		}

		state.Installed[modID] = InstalledRecord{
			Filename:  filename,
			Version:   release.ModVersion,
			ModDBID:   mod.ModID,
			AssetID:   mod.AssetID,
			SyncedUTC: time.Now().UTC().Format(time.RFC3339),
		}
	}

	if prune {
		for modID, rec := range state.Installed {
			if _, ok := desiredIDs[modID]; ok {
				continue
			}
			if strings.TrimSpace(rec.Filename) != "" {
				path := filepath.Join(paths.ModDir, rec.Filename)
				if fileExistsNonZero(path) {
					if dryRun {
						result.Logs = append(result.Logs, fmt.Sprintf("[dry-run] would prune %s", rec.Filename))
					} else if err := os.Remove(path); err != nil {
						result.Failed++
						result.Logs = append(result.Logs, fmt.Sprintf("[err] failed to prune %s: %v", rec.Filename, err))
					} else {
						result.Removed++
						result.Logs = append(result.Logs, fmt.Sprintf("[prune] removed %s", rec.Filename))
					}
				}
			}
			if !dryRun {
				delete(state.Installed, modID)
			}
		}
	}

	state.LastSyncUTC = time.Now().UTC().Format(time.RFC3339)
	if !dryRun {
		if err := saveState(paths.StateFile, state); err != nil {
			return result, err
		}
	}

	return result, nil
}

func sanitizeBackupName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "mods-backup-" + time.Now().UTC().Format("20060102-150405")
	}
	re := regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	name = re.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "mods-backup-" + time.Now().UTC().Format("20060102-150405")
	}
	return name
}

func createBackup(modDir string, backupDir string, name string) (string, error) {
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	backupPath := filepath.Join(backupDir, sanitizeBackupName(name)+".zip")
	file, err := os.Create(backupPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	zipWriter := zip.NewWriter(file)
	defer zipWriter.Close()

	entries, err := os.ReadDir(modDir)
	if err != nil {
		return "", err
	}

	files := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, entry.Name())
		srcPath := filepath.Join(modDir, entry.Name())
		src, err := os.Open(srcPath)
		if err != nil {
			return "", err
		}
		w, err := zipWriter.Create("mods/" + entry.Name())
		if err != nil {
			src.Close()
			return "", err
		}
		if _, err := io.Copy(w, src); err != nil {
			src.Close()
			return "", err
		}
		src.Close()
	}

	meta := map[string]any{
		"created_utc": time.Now().UTC().Format(time.RFC3339),
		"mod_dir":     modDir,
		"file_count":  len(files),
		"files":       files,
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", err
	}
	w, err := zipWriter.Create("_metadata.json")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(w, bytes.NewReader(metaBytes)); err != nil {
		return "", err
	}

	if err := zipWriter.Close(); err != nil {
		return "", err
	}
	return backupPath, nil
}

type backupInfo struct {
	Path  string
	Name  string
	Size  int64
	MTime time.Time
}

func listBackups(backupDir string) ([]backupInfo, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []backupInfo{}, nil
		}
		return nil, err
	}
	out := []backupInfo{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.ToLower(filepath.Ext(entry.Name())) != ".zip" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, backupInfo{
			Path:  filepath.Join(backupDir, entry.Name()),
			Name:  entry.Name(),
			Size:  info.Size(),
			MTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MTime.After(out[j].MTime) })
	return out, nil
}

func restoreBackup(backupPath string, modDir string, clean bool) (int, error) {
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		return 0, err
	}
	if clean {
		entries, err := os.ReadDir(modDir)
		if err != nil {
			return 0, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if err := os.Remove(filepath.Join(modDir, e.Name())); err != nil {
				return 0, err
			}
		}
	}

	zr, err := zip.OpenReader(backupPath)
	if err != nil {
		return 0, err
	}
	defer zr.Close()

	restored := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !strings.HasPrefix(f.Name, "mods/") {
			continue
		}
		base := filepath.Base(f.Name)
		if base == "." || base == "" {
			continue
		}
		outPath := filepath.Join(modDir, base)

		src, err := f.Open()
		if err != nil {
			return restored, err
		}
		dst, err := os.Create(outPath)
		if err != nil {
			src.Close()
			return restored, err
		}
		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			dst.Close()
			return restored, err
		}
		src.Close()
		if err := dst.Close(); err != nil {
			return restored, err
		}
		restored++
	}
	return restored, nil
}

func orderByFromSort(sortMode string) string {
	switch strings.ToLower(strings.TrimSpace(sortMode)) {
	case "downloads":
		return "downloads"
	case "trending":
		return "trendingpoints"
	case "comments":
		return "comments"
	case "name":
		return "name"
	default:
		return "lastreleased"
	}
}

func printSearchResults(mods []SearchMod) {
	if len(mods) == 0 {
		fmt.Println("No mods found.")
		return
	}
	fmt.Println(" #  modid   identifier               downloads  name")
	fmt.Println("--  ------  -----------------------  ---------  ----------------------------------------------")
	for i, mod := range mods {
		ident := ""
		if len(mod.ModIDStrs) > 0 {
			ident = mod.ModIDStrs[0]
		}
		fmt.Printf("%2d  %6d  %-23s  %9d  %s\n", i+1, mod.ModID, trimTo(ident, 23), mod.Downloads, trimTo(mod.Name, 46))
	}
}

func printReleases(mod *APIMod, limit int) {
	releases := sortedReleases(mod)
	if limit > 0 && limit < len(releases) {
		releases = releases[:limit]
	}
	if len(releases) == 0 {
		fmt.Println("No releases listed.")
		return
	}

	fmt.Println(" #  version        created              tags")
	fmt.Println("--  -------------  -------------------  ----------------------------------------------")
	for i, r := range releases {
		tagText := strings.Join(r.Tags, ", ")
		fmt.Printf("%2d  %-13s  %-19s  %s\n", i+1, trimTo(r.ModVersion, 13), trimTo(r.Created, 19), trimTo(tagText, 46))
	}
}

func trimTo(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseWithFlagSet(fs *flag.FlagSet, args []string) bool {
	fs.SetOutput(os.Stderr)
	normalized := normalizeFlagArgs(fs, args)
	if err := fs.Parse(normalized); err != nil {
		return false
	}
	return true
}

type boolFlag interface {
	IsBoolFlag() bool
}

// normalizeFlagArgs allows global-style flags after positional args,
// e.g. `vsmm add modid --config file.json`.
func normalizeFlagArgs(fs *flag.FlagSet, args []string) []string {
	boolFlags := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(boolFlag); ok && bf.IsBoolFlag() {
			boolFlags[f.Name] = true
		}
	})

	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		flags = append(flags, arg)
		if strings.Contains(arg, "=") {
			continue
		}

		name := strings.TrimLeft(arg, "-")
		isBool := boolFlags[name]
		if !isBool && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			flags = append(flags, args[i+1])
			i++
		}
	}

	return append(flags, positionals...)
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	cfgFile := fs.String("config", defaultCfgPath, "config file path")
	modDir := fs.String("mod-dir", "", "Vintage Story mods directory")
	gameVersion := fs.String("game-version", "", "default game version preference")
	force := fs.Bool("force", false, "overwrite existing config")
	if !parseWithFlagSet(fs, args) {
		return 1
	}

	cfgPath := absConfigPath(*cfgFile)
	if _, err := os.Stat(cfgPath); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "config already exists: %s (use --force to overwrite)\n", cfgPath)
		return 1
	}

	cfg := newConfig(strings.TrimSpace(*modDir), strings.TrimSpace(*gameVersion))
	if err := saveConfig(cfgPath, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error writing config: %v\n", err)
		return 1
	}
	fmt.Printf("Created config: %s\n", cfgPath)
	fmt.Printf("Default mod dir: %s\n", cfg.ModDir)
	return 0
}

func cmdSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "max results")
	sortMode := fs.String("sort", "recent", "result order: recent|downloads|trending|comments|name")
	if !parseWithFlagSet(fs, args) {
		return 1
	}

	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "search requires query text")
		return 1
	}

	mods, err := searchMods(query, *limit, orderByFromSort(*sortMode))
	if err != nil {
		fmt.Fprintf(os.Stderr, "search failed: %v\n", err)
		return 1
	}
	printSearchResults(mods)
	return 0
}

func cmdShow(args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	releases := fs.Int("releases", 15, "number of releases to show")
	if !parseWithFlagSet(fs, args) {
		return 1
	}
	if len(fs.Args()) < 1 {
		fmt.Fprintln(os.Stderr, "show requires a mod reference")
		return 1
	}
	ref := strings.TrimSpace(fs.Args()[0])
	mod, err := getMod(ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "show failed: %v\n", err)
		return 1
	}
	fmt.Printf("Name:      %s\n", mod.Name)
	fmt.Printf("ID:        %d\n", mod.ModID)
	fmt.Printf("Asset ID:  %d\n", mod.AssetID)
	fmt.Printf("URL:       %s\n", modHomeURL(mod))
	fmt.Printf("Author:    %s\n", mod.Author)
	fmt.Printf("Downloads: %d\n", mod.Downloads)
	fmt.Println()
	printReleases(mod, *releases)
	return 0
}

func cmdAdd(args []string) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	cfgFile := fs.String("config", defaultCfgPath, "config file path")
	version := fs.String("version", "", "pin explicit release version")
	pin := fs.Bool("pin", false, "pin latest resolved release now")
	gameVersion := fs.String("game-version", "", "override game version for this mod")
	if !parseWithFlagSet(fs, args) {
		return 1
	}
	if len(fs.Args()) < 1 {
		fmt.Fprintln(os.Stderr, "add requires a mod reference")
		return 1
	}
	ref := strings.TrimSpace(fs.Args()[0])
	cfgPath := absConfigPath(*cfgFile)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printLoadConfigError(err, cfgPath)
		return 1
	}

	mod, err := getMod(ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve mod: %v\n", err)
		return 1
	}
	identifier := primaryIdentifier(mod)
	if identifier == "" {
		fmt.Fprintln(os.Stderr, "failed to resolve mod identifier")
		return 1
	}

	existingIdx := findModIndex(cfg, identifier)
	var existing *ModEntry
	if existingIdx >= 0 {
		tmp := cfg.Mods[existingIdx]
		existing = &tmp
	}

	gameVersionSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "game-version" {
			gameVersionSet = true
		}
	})

	selectedGV := ""
	if gameVersionSet {
		selectedGV = strings.TrimSpace(*gameVersion)
	} else if existing != nil {
		selectedGV = strings.TrimSpace(existing.GameVersion)
	}

	strategy := "latest"
	pinned := ""
	if strings.TrimSpace(*version) != "" {
		strategy = "pinned"
		pinned = strings.TrimSpace(*version)
		if _, err := pickRelease(mod, pinned, selectedGV); err != nil {
			fmt.Fprintf(os.Stderr, "invalid version selection: %v\n", err)
			return 1
		}
	} else if *pin {
		strategy = "pinned"
		rel, err := pickRelease(mod, "", selectedGV)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to resolve release: %v\n", err)
			return 1
		}
		pinned = strings.TrimSpace(rel.ModVersion)
	} else {
		if _, err := pickRelease(mod, "", selectedGV); err != nil {
			fmt.Fprintf(os.Stderr, "failed to resolve latest release: %v\n", err)
			return 1
		}
	}

	entry := ModEntry{}
	if existing != nil {
		entry = *existing
	}
	entry.ID = identifier
	entry.ModDBID = mod.ModID
	entry.AssetID = mod.AssetID
	entry.Name = mod.Name
	entry.SourceURL = modHomeURL(mod)
	entry.Strategy = strategy
	if strategy == "pinned" {
		entry.PinnedVersion = pinned
	} else {
		entry.PinnedVersion = ""
	}
	if gameVersionSet {
		entry.GameVersion = selectedGV
	}

	upsertMod(cfg, entry)
	if err := saveConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "failed to save config: %v\n", err)
		return 1
	}

	if strategy == "pinned" {
		fmt.Printf("Added/updated %s pinned to %s.\n", entry.ID, entry.PinnedVersion)
	} else {
		fmt.Printf("Added/updated %s to track latest release.\n", entry.ID)
	}
	return 0
}

func cmdBrowse(args []string) int {
	fs := flag.NewFlagSet("browse", flag.ContinueOnError)
	cfgFile := fs.String("config", defaultCfgPath, "config file path")
	limit := fs.Int("limit", 20, "max results")
	sortMode := fs.String("sort", "recent", "result order")
	if !parseWithFlagSet(fs, args) {
		return 1
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "browse requires query text")
		return 1
	}

	cfgPath := absConfigPath(*cfgFile)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printLoadConfigError(err, cfgPath)
		return 1
	}

	mods, err := searchMods(query, *limit, orderByFromSort(*sortMode))
	if err != nil {
		fmt.Fprintf(os.Stderr, "search failed: %v\n", err)
		return 1
	}
	if len(mods) == 0 {
		fmt.Println("No mods found.")
		return 0
	}
	printSearchResults(mods)

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Select mod # to add (blank to cancel): ")
	rawSel, _ := reader.ReadString('\n')
	rawSel = strings.TrimSpace(rawSel)
	if rawSel == "" {
		fmt.Println("Cancelled.")
		return 0
	}
	idx, err := strconv.Atoi(rawSel)
	if err != nil || idx < 1 || idx > len(mods) {
		fmt.Fprintln(os.Stderr, "invalid selection")
		return 1
	}

	selected := mods[idx-1]
	mod, err := getMod(strconv.Itoa(selected.ModID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load selected mod: %v\n", err)
		return 1
	}

	identifier := primaryIdentifier(mod)
	if identifier == "" {
		fmt.Fprintln(os.Stderr, "selected mod has no identifier")
		return 1
	}

	fmt.Println()
	fmt.Printf("Selected: %s (%s)\n", mod.Name, identifier)
	printReleases(mod, 12)
	fmt.Println()
	fmt.Print("Track latest release? [Y/n]: ")
	mode, _ := reader.ReadString('\n')
	mode = strings.ToLower(strings.TrimSpace(mode))

	strategy := "latest"
	pinned := ""
	if mode == "n" || mode == "no" {
		releases := sortedReleases(mod)
		fmt.Print("Release # to pin (default 1): ")
		rawRelease, _ := reader.ReadString('\n')
		rawRelease = strings.TrimSpace(rawRelease)
		if rawRelease == "" {
			rawRelease = "1"
		}
		relIdx, err := strconv.Atoi(rawRelease)
		if err != nil || relIdx < 1 || relIdx > len(releases) {
			fmt.Fprintln(os.Stderr, "invalid release index")
			return 1
		}
		strategy = "pinned"
		pinned = strings.TrimSpace(releases[relIdx-1].ModVersion)
	}

	existingIdx := findModIndex(cfg, identifier)
	entry := ModEntry{}
	if existingIdx >= 0 {
		entry = cfg.Mods[existingIdx]
	}
	entry.ID = identifier
	entry.ModDBID = mod.ModID
	entry.AssetID = mod.AssetID
	entry.Name = mod.Name
	entry.SourceURL = modHomeURL(mod)
	entry.Strategy = strategy
	entry.PinnedVersion = pinned
	if strategy != "pinned" {
		entry.PinnedVersion = ""
	}
	upsertMod(cfg, entry)

	if err := saveConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "failed to save config: %v\n", err)
		return 1
	}

	if strategy == "pinned" {
		fmt.Printf("Saved %s pinned to %s.\n", identifier, pinned)
	} else {
		fmt.Printf("Saved %s tracking latest.\n", identifier)
	}
	return 0
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	cfgFile := fs.String("config", defaultCfgPath, "config file path")
	if !parseWithFlagSet(fs, args) {
		return 1
	}
	cfgPath := absConfigPath(*cfgFile)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printLoadConfigError(err, cfgPath)
		return 1
	}
	if len(cfg.Mods) == 0 {
		fmt.Println("No configured mods.")
		return 0
	}
	fmt.Println("Configured mods:")
	for _, entry := range cfg.Mods {
		gv := strings.TrimSpace(entry.GameVersion)
		if gv == "" {
			gv = "*"
		}
		strategy := strings.ToLower(strings.TrimSpace(entry.Strategy))
		if strategy == "pinned" {
			fmt.Printf("- %s (pinned %s) gv=%s\n", entry.ID, entry.PinnedVersion, gv)
		} else {
			fmt.Printf("- %s (latest) gv=%s\n", entry.ID, gv)
		}
	}
	return 0
}

func cmdRemove(args []string) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	cfgFile := fs.String("config", defaultCfgPath, "config file path")
	if !parseWithFlagSet(fs, args) {
		return 1
	}
	if len(fs.Args()) < 1 {
		fmt.Fprintln(os.Stderr, "remove requires mod id")
		return 1
	}
	modID := strings.TrimSpace(fs.Args()[0])
	cfgPath := absConfigPath(*cfgFile)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printLoadConfigError(err, cfgPath)
		return 1
	}
	if !removeMod(cfg, modID) {
		fmt.Fprintf(os.Stderr, "not found in config: %s\n", modID)
		return 1
	}
	if err := saveConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "failed to save config: %v\n", err)
		return 1
	}
	fmt.Printf("Removed %s\n", modID)
	return 0
}

func cmdConfig(args []string) int {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	cfgFile := fs.String("config", defaultCfgPath, "config file path")
	modDir := fs.String("mod-dir", "", "set mod directory")
	cacheDir := fs.String("cache-dir", "", "set cache directory")
	backupDir := fs.String("backup-dir", "", "set backup directory")
	gameVersion := fs.String("game-version", "", "set default game version (empty clears)")
	if !parseWithFlagSet(fs, args) {
		return 1
	}

	cfgPath := absConfigPath(*cfgFile)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printLoadConfigError(err, cfgPath)
		return 1
	}

	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})

	updated := false
	if visited["mod-dir"] {
		cfg.ModDir = strings.TrimSpace(*modDir)
		updated = true
	}
	if visited["cache-dir"] {
		cfg.CacheDir = strings.TrimSpace(*cacheDir)
		updated = true
	}
	if visited["backup-dir"] {
		cfg.BackupDir = strings.TrimSpace(*backupDir)
		updated = true
	}
	if visited["game-version"] {
		cfg.GameVersion = strings.TrimSpace(*gameVersion)
		updated = true
	}

	if updated {
		if err := saveConfig(cfgPath, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "failed to save config: %v\n", err)
			return 1
		}
		fmt.Printf("Updated config: %s\n", cfgPath)
	}

	paths := getPaths(cfg, cfgPath)
	fmt.Printf("config:       %s\n", cfgPath)
	fmt.Printf("mod_dir:      %s\n", paths.ModDir)
	fmt.Printf("cache_dir:    %s\n", paths.CacheDir)
	fmt.Printf("backup_dir:   %s\n", paths.BackupDir)
	if strings.TrimSpace(cfg.GameVersion) == "" {
		fmt.Println("game_version: (unset)")
	} else {
		fmt.Printf("game_version: %s\n", cfg.GameVersion)
	}
	return 0
}

func cmdSync(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	cfgFile := fs.String("config", defaultCfgPath, "config file path")
	dryRun := fs.Bool("dry-run", false, "show actions without downloading")
	prune := fs.Bool("prune", false, "remove managed files no longer in config")
	backupFirst := fs.Bool("backup-first", false, "create backup before syncing")
	if !parseWithFlagSet(fs, args) {
		return 1
	}

	cfgPath := absConfigPath(*cfgFile)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printLoadConfigError(err, cfgPath)
		return 1
	}
	paths := getPaths(cfg, cfgPath)

	result, err := syncMods(cfg, paths, *dryRun, *prune, *backupFirst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync failed: %v\n", err)
		return 1
	}
	for _, line := range result.Logs {
		fmt.Println(line)
	}
	fmt.Printf("Summary: downloaded=%d, skipped=%d, removed=%d, failed=%d\n", result.Downloaded, result.Skipped, result.Removed, result.Failed)
	if result.Failed > 0 {
		return 1
	}
	return 0
}

func cmdBackupCreate(args []string) int {
	fs := flag.NewFlagSet("backup-create", flag.ContinueOnError)
	cfgFile := fs.String("config", defaultCfgPath, "config file path")
	name := fs.String("name", "", "backup name (without .zip)")
	if !parseWithFlagSet(fs, args) {
		return 1
	}
	cfgPath := absConfigPath(*cfgFile)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printLoadConfigError(err, cfgPath)
		return 1
	}
	paths := getPaths(cfg, cfgPath)
	backupPath, err := createBackup(paths.ModDir, paths.BackupDir, *name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup failed: %v\n", err)
		return 1
	}
	fmt.Printf("Backup created: %s\n", backupPath)
	return 0
}

func cmdBackupList(args []string) int {
	fs := flag.NewFlagSet("backup-list", flag.ContinueOnError)
	cfgFile := fs.String("config", defaultCfgPath, "config file path")
	if !parseWithFlagSet(fs, args) {
		return 1
	}
	cfgPath := absConfigPath(*cfgFile)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printLoadConfigError(err, cfgPath)
		return 1
	}
	paths := getPaths(cfg, cfgPath)
	items, err := listBackups(paths.BackupDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup list failed: %v\n", err)
		return 1
	}
	if len(items) == 0 {
		fmt.Println("No backups found.")
		return 0
	}
	fmt.Println("Backups:")
	for _, item := range items {
		sizeMB := float64(item.Size) / (1024 * 1024)
		fmt.Printf("- %s (%.2f MiB, %s)\n", item.Name, sizeMB, item.MTime.Format(time.RFC3339))
	}
	return 0
}

func cmdBackupRestore(args []string) int {
	fs := flag.NewFlagSet("backup-restore", flag.ContinueOnError)
	cfgFile := fs.String("config", defaultCfgPath, "config file path")
	clean := fs.Bool("clean", false, "delete existing files before restore")
	if !parseWithFlagSet(fs, args) {
		return 1
	}
	if len(fs.Args()) < 1 {
		fmt.Fprintln(os.Stderr, "backup-restore requires backup filename or path")
		return 1
	}
	backupArg := expandUserPath(strings.TrimSpace(fs.Args()[0]))
	cfgPath := absConfigPath(*cfgFile)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printLoadConfigError(err, cfgPath)
		return 1
	}
	paths := getPaths(cfg, cfgPath)

	backupPath := backupArg
	if !filepath.IsAbs(backupPath) {
		backupPath = filepath.Join(paths.BackupDir, backupArg)
	}

	restored, err := restoreBackup(backupPath, paths.ModDir, *clean)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore failed: %v\n", err)
		return 1
	}
	fmt.Printf("Restored %d files from %s\n", restored, backupPath)
	return 0
}
