package cmd

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
	gapi "google.golang.org/api/googleapi"

	"github.com/steipete/gogcli/internal/config"
	"github.com/steipete/gogcli/internal/outfmt"
	"github.com/steipete/gogcli/internal/ui"
)

const (
	driveSyncStateFile = ".gog-sync.json"
	driveSyncVersion   = 1
	timeSkewTolerance  = 2 * time.Second
)

type DriveSyncCmd struct {
	Pull DriveSyncPullCmd `cmd:"" name:"pull" help:"Sync Drive folder to local"`
	Push DriveSyncPushCmd `cmd:"" name:"push" help:"Sync local folder to Drive"`
}

type DriveSyncPullCmd struct {
	Folder   string   `name:"folder" help:"Drive folder ID (required if no state file)"`
	Out      string   `name:"out" help:"Local destination directory (required if no state file)"`
	State    string   `name:"state" help:"Path to sync state file (default: <out>/.gog-sync.json)"`
	Delete   bool     `name:"delete" help:"Delete local files not present in Drive"`
	Checksum bool     `name:"checksum" help:"Use checksums to detect changes"`
	Include  []string `name:"include" help:"Include glob (repeatable)"`
	Exclude  []string `name:"exclude" help:"Exclude glob (repeatable)"`
}

func (c *DriveSyncPullCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)
	account, err := requireAccount(flags)
	if err != nil {
		return err
	}

	statePath, rootPath, cfg, err := loadDriveSyncConfig(c.State, c.Out, "pull", account)
	if err != nil {
		return err
	}
	if strings.TrimSpace(c.Folder) != "" {
		cfg.FolderID = strings.TrimSpace(c.Folder)
	}
	if len(c.Include) > 0 {
		cfg.Include = c.Include
	}
	if len(c.Exclude) > 0 {
		cfg.Exclude = c.Exclude
	}
	cfg.Exclude = ensureDriveSyncExcludes(cfg.Exclude)
	if cfg.FolderID == "" || rootPath == "" {
		return usage("missing --folder or --out (or state file)")
	}
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		return err
	}

	svc, err := newDriveService(ctx, account)
	if err != nil {
		return err
	}

	remoteItems, _, err := listDriveTree(ctx, svc, driveTreeOptions{
		RootID:        cfg.FolderID,
		MaxDepth:      0,
		MaxItems:      0,
		Fields:        driveSyncFields,
		IncludeFiles:  true,
		IncludeFolder: true,
	})
	if err != nil {
		return err
	}

	remoteFiles, remoteFolders := splitDriveItems(remoteItems, true)
	localFiles, err := walkLocalFiles(rootPath, cfg.Include, cfg.Exclude, c.Checksum)
	if err != nil {
		return err
	}

	plan := buildDrivePullPlan(remoteFiles, remoteFolders, localFiles, cfg, c.Delete, c.Checksum)
	if err := outputDriveSyncPlan(ctx, u, plan); err != nil {
		return err
	}

	if flags != nil && flags.DryRun {
		return nil
	}

	if plan.HasDeletes() {
		if err := confirmDestructive(ctx, flags, "delete local files"); err != nil {
			return err
		}
	}

	if err := applyDrivePullPlan(ctx, svc, rootPath, plan); err != nil {
		return err
	}

	return saveDriveSyncState(statePath, cfg)
}

type DriveSyncPushCmd struct {
	Folder   string   `name:"folder" help:"Drive folder ID (required if no state file)"`
	From     string   `name:"from" help:"Local source directory (required if no state file)"`
	State    string   `name:"state" help:"Path to sync state file (default: <from>/.gog-sync.json)"`
	Delete   bool     `name:"delete" help:"Delete Drive files not present locally"`
	Checksum bool     `name:"checksum" help:"Use checksums to detect changes"`
	Include  []string `name:"include" help:"Include glob (repeatable)"`
	Exclude  []string `name:"exclude" help:"Exclude glob (repeatable)"`
}

func (c *DriveSyncPushCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)
	account, err := requireAccount(flags)
	if err != nil {
		return err
	}

	statePath, rootPath, cfg, err := loadDriveSyncConfig(c.State, c.From, "push", account)
	if err != nil {
		return err
	}
	if strings.TrimSpace(c.Folder) != "" {
		cfg.FolderID = strings.TrimSpace(c.Folder)
	}
	if len(c.Include) > 0 {
		cfg.Include = c.Include
	}
	if len(c.Exclude) > 0 {
		cfg.Exclude = c.Exclude
	}
	cfg.Exclude = ensureDriveSyncExcludes(cfg.Exclude)
	if cfg.FolderID == "" || rootPath == "" {
		return usage("missing --folder or --from (or state file)")
	}

	svc, err := newDriveService(ctx, account)
	if err != nil {
		return err
	}

	remoteItems, _, err := listDriveTree(ctx, svc, driveTreeOptions{
		RootID:        cfg.FolderID,
		MaxDepth:      0,
		MaxItems:      0,
		Fields:        driveSyncFields,
		IncludeFiles:  true,
		IncludeFolder: true,
	})
	if err != nil {
		return err
	}

	remoteFiles, remoteFolders := splitDriveItems(remoteItems, false)
	localFiles, err := walkLocalFiles(rootPath, cfg.Include, cfg.Exclude, c.Checksum)
	if err != nil {
		return err
	}

	plan := buildDrivePushPlan(remoteFiles, localFiles, cfg, c.Delete, c.Checksum)
	if err := outputDriveSyncPlan(ctx, u, plan); err != nil {
		return err
	}

	if flags != nil && flags.DryRun {
		return nil
	}

	if plan.HasDeletes() {
		if err := confirmDestructive(ctx, flags, "delete Drive files"); err != nil {
			return err
		}
	}

	if err := applyDrivePushPlan(ctx, svc, cfg.FolderID, rootPath, remoteFolders, plan); err != nil {
		return err
	}

	return saveDriveSyncState(statePath, cfg)
}

type driveSyncConfig struct {
	Version   int      `json:"version"`
	Direction string   `json:"direction"`
	Account   string   `json:"account"`
	FolderID  string   `json:"folderId"`
	LocalRoot string   `json:"localRoot"`
	Include   []string `json:"include,omitempty"`
	Exclude   []string `json:"exclude,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
}

func loadDriveSyncConfig(statePath string, rootPath string, direction string, account string) (string, string, driveSyncConfig, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath != "" {
		expanded, err := config.ExpandPath(rootPath)
		if err != nil {
			return "", "", driveSyncConfig{}, err
		}
		rootPath = expanded
	}

	statePath, err := resolveDriveSyncStatePath(statePath, rootPath)
	if err != nil {
		return "", "", driveSyncConfig{}, err
	}

	cfg := driveSyncConfig{
		Version:   driveSyncVersion,
		Direction: direction,
		Account:   account,
		LocalRoot: rootPath,
		Exclude:   []string{driveSyncStateFile},
	}

	if statePath == "" {
		return "", rootPath, cfg, nil
	}

	if data, err := os.ReadFile(statePath); err == nil {
		var stored driveSyncConfig
		if jsonErr := json.Unmarshal(data, &stored); jsonErr == nil {
			if cfg.FolderID == "" {
				cfg.FolderID = stored.FolderID
			}
			if cfg.LocalRoot == "" {
				cfg.LocalRoot = stored.LocalRoot
				rootPath = stored.LocalRoot
			}
			if len(cfg.Include) == 0 {
				cfg.Include = stored.Include
			}
			cfg.Exclude = append(cfg.Exclude, stored.Exclude...)
		}
	} else if !os.IsNotExist(err) {
		return "", "", driveSyncConfig{}, fmt.Errorf("read sync state: %w", err)
	}

	return statePath, rootPath, cfg, nil
}

func resolveDriveSyncStatePath(explicit string, rootPath string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		expanded, err := config.ExpandPath(explicit)
		if err != nil {
			return "", err
		}
		return expanded, nil
	}
	if rootPath == "" {
		return "", nil
	}
	return filepath.Join(rootPath, driveSyncStateFile), nil
}

func saveDriveSyncState(path string, cfg driveSyncConfig) error {
	if path == "" {
		return nil
	}
	cfg.Version = driveSyncVersion
	cfg.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sync state: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write sync state: %w", err)
	}
	return nil
}

type localFileInfo struct {
	Path    string
	Full    string
	Size    int64
	ModTime time.Time
	MD5     string
}

const driveSyncFields = "id,name,mimeType,size,modifiedTime,md5Checksum"

func splitDriveItems(items []driveTreeItem, exportDocs bool) (map[string]driveTreeItem, map[string]driveTreeItem) {
	files := map[string]driveTreeItem{}
	folders := map[string]driveTreeItem{}
	for _, it := range items {
		if it.IsFolder() {
			folders[it.Path] = it
			continue
		}
		path := it.Path
		if exportDocs && strings.HasPrefix(it.MimeType, "application/vnd.google-apps.") {
			exportExt := driveExportExtension(driveExportMimeType(it.MimeType))
			path = replaceExt(path, exportExt)
		}
		it.Path = path
		files[path] = it
	}
	return files, folders
}

func walkLocalFiles(root string, includes []string, excludes []string, checksum bool) (map[string]localFileInfo, error) {
	files := map[string]localFileInfo{}
	if root == "" {
		return files, nil
	}

	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("sync root is not a directory: %s", root)
	}

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if !allowSyncPath(rel, nil, excludes) && len(includes) == 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if !allowSyncPath(rel, includes, excludes) {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		entry := localFileInfo{
			Path:    rel,
			Full:    path,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}
		if checksum {
			if sum, sumErr := fileMD5(path); sumErr == nil {
				entry.MD5 = sum
			}
		}
		files[rel] = entry
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func allowSyncPath(rel string, includes []string, excludes []string) bool {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return true
	}
	if len(includes) > 0 {
		allowed := false
		for _, pattern := range includes {
			if patternMatch(pattern, rel) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	for _, pattern := range excludes {
		if patternMatch(pattern, rel) {
			return false
		}
	}
	return true
}

func patternMatch(pattern string, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	ok, err := path.Match(pattern, value)
	return err == nil && ok
}

func ensureDriveSyncExcludes(excludes []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(excludes)+1)
	for _, ex := range excludes {
		ex = strings.TrimSpace(ex)
		if ex == "" || seen[ex] {
			continue
		}
		seen[ex] = true
		out = append(out, ex)
	}
	if !seen[driveSyncStateFile] {
		out = append(out, driveSyncStateFile)
	}
	return out
}

func fileMD5(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // user-provided path
	if err != nil {
		return "", err
	}
	defer f.Close()

	hash := md5.New() //nolint:gosec // non-cryptographic checksum
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type driveSyncAction struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	Drive    string `json:"driveId,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type driveSyncPlan struct {
	Actions []driveSyncAction `json:"actions"`
	Summary driveSyncSummary  `json:"summary"`
}

type driveSyncSummary struct {
	Download    int `json:"download"`
	Upload      int `json:"upload"`
	DeleteLocal int `json:"deleteLocal"`
	DeleteDrive int `json:"deleteDrive"`
	MkdirLocal  int `json:"mkdirLocal"`
	MkdirDrive  int `json:"mkdirDrive"`
}

func (p driveSyncPlan) HasDeletes() bool {
	return p.Summary.DeleteLocal > 0 || p.Summary.DeleteDrive > 0
}

func buildDrivePullPlan(remoteFiles map[string]driveTreeItem, remoteFolders map[string]driveTreeItem, localFiles map[string]localFileInfo, cfg driveSyncConfig, allowDelete bool, checksum bool) driveSyncPlan {
	plan := driveSyncPlan{}
	seenLocal := map[string]bool{}

	for relPath, remote := range remoteFiles {
		if !allowSyncPath(relPath, cfg.Include, cfg.Exclude) {
			continue
		}
		local, ok := localFiles[relPath]
		if !ok {
			plan.Actions = append(plan.Actions, driveSyncAction{Type: "download", Path: relPath, Drive: remote.ID, MimeType: remote.MimeType, Reason: "missing"})
			plan.Summary.Download++
			ensurePlanDirs(&plan, path.Dir(relPath), "mkdir_local")
			continue
		}
		seenLocal[relPath] = true
		if needsPull(remote, local, checksum) {
			plan.Actions = append(plan.Actions, driveSyncAction{Type: "download", Path: relPath, Drive: remote.ID, MimeType: remote.MimeType, Reason: "changed"})
			plan.Summary.Download++
			ensurePlanDirs(&plan, path.Dir(relPath), "mkdir_local")
		}
	}

	if allowDelete {
		for relPath := range localFiles {
			if !allowSyncPath(relPath, cfg.Include, cfg.Exclude) {
				continue
			}
			if _, ok := remoteFiles[relPath]; ok {
				continue
			}
			if seenLocal[relPath] {
				continue
			}
			plan.Actions = append(plan.Actions, driveSyncAction{Type: "delete_local", Path: relPath, Reason: "not in Drive"})
			plan.Summary.DeleteLocal++
		}
	}

	for folderPath := range remoteFolders {
		if !allowSyncPath(folderPath, cfg.Include, cfg.Exclude) {
			continue
		}
		if folderPath == "" {
			continue
		}
		ensurePlanDirs(&plan, folderPath, "mkdir_local")
	}

	return plan
}

func buildDrivePushPlan(remoteFiles map[string]driveTreeItem, localFiles map[string]localFileInfo, cfg driveSyncConfig, allowDelete bool, checksum bool) driveSyncPlan {
	plan := driveSyncPlan{}
	seenRemote := map[string]bool{}

	for relPath, local := range localFiles {
		if !allowSyncPath(relPath, cfg.Include, cfg.Exclude) {
			continue
		}
		remote, ok := remoteFiles[relPath]
		if !ok {
			plan.Actions = append(plan.Actions, driveSyncAction{Type: "upload", Path: relPath, Reason: "missing"})
			plan.Summary.Upload++
			ensurePlanDirs(&plan, path.Dir(relPath), "mkdir_drive")
			continue
		}
		seenRemote[relPath] = true
		if needsPush(remote, local, checksum) {
			plan.Actions = append(plan.Actions, driveSyncAction{Type: "upload", Path: relPath, Drive: remote.ID, Reason: "changed"})
			plan.Summary.Upload++
		}
	}

	if allowDelete {
		for relPath, remote := range remoteFiles {
			if !allowSyncPath(relPath, cfg.Include, cfg.Exclude) {
				continue
			}
			if _, ok := localFiles[relPath]; ok {
				continue
			}
			if seenRemote[relPath] {
				continue
			}
			plan.Actions = append(plan.Actions, driveSyncAction{Type: "delete_drive", Path: relPath, Drive: remote.ID, Reason: "not local"})
			plan.Summary.DeleteDrive++
		}
	}

	return plan
}

func ensurePlanDirs(plan *driveSyncPlan, dir string, actionType string) {
	dir = strings.TrimSpace(dir)
	for dir != "" && dir != "." && dir != "/" {
		if !hasAction(plan.Actions, actionType, dir) {
			plan.Actions = append(plan.Actions, driveSyncAction{Type: actionType, Path: dir})
			switch actionType {
			case "mkdir_local":
				plan.Summary.MkdirLocal++
			case "mkdir_drive":
				plan.Summary.MkdirDrive++
			}
		}
		next := path.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
}

func hasAction(actions []driveSyncAction, actionType string, path string) bool {
	for _, a := range actions {
		if a.Type == actionType && a.Path == path {
			return true
		}
	}
	return false
}

func needsPull(remote driveTreeItem, local localFileInfo, checksum bool) bool {
	if checksum && remote.MD5 != "" && local.MD5 != "" {
		if remote.MD5 != local.MD5 {
			return true
		}
		return false
	}
	if remote.Size > 0 && remote.Size != local.Size {
		return true
	}
	remoteTime, err := parseDriveTime(remote.ModifiedTime)
	if err != nil {
		return false
	}
	return remoteTime.After(local.ModTime.Add(timeSkewTolerance))
}

func needsPush(remote driveTreeItem, local localFileInfo, checksum bool) bool {
	if strings.HasPrefix(remote.MimeType, "application/vnd.google-apps.") {
		return false
	}
	if checksum && remote.MD5 != "" && local.MD5 != "" {
		if remote.MD5 != local.MD5 {
			return true
		}
		return false
	}
	if remote.Size > 0 && remote.Size != local.Size {
		return true
	}
	remoteTime, err := parseDriveTime(remote.ModifiedTime)
	if err != nil {
		return true
	}
	return local.ModTime.After(remoteTime.Add(timeSkewTolerance))
}

func parseDriveTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("empty time")
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, raw)
}

func outputDriveSyncPlan(ctx context.Context, u *ui.UI, plan driveSyncPlan) error {
	if outfmt.IsJSON(ctx) {
		return outfmt.WriteJSON(ctx, os.Stdout, plan)
	}

	w, flush := tableWriter(ctx)
	defer flush()
	fmt.Fprintln(w, "ACTION\tPATH")
	for _, action := range plan.Actions {
		if action.Type == "" {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\n", action.Type, sanitizeTab(action.Path))
	}
	if u != nil {
		u.Err().Printf("downloads\t%d", plan.Summary.Download)
		u.Err().Printf("uploads\t%d", plan.Summary.Upload)
		u.Err().Printf("delete_local\t%d", plan.Summary.DeleteLocal)
		u.Err().Printf("delete_drive\t%d", plan.Summary.DeleteDrive)
		u.Err().Printf("mkdir_local\t%d", plan.Summary.MkdirLocal)
		u.Err().Printf("mkdir_drive\t%d", plan.Summary.MkdirDrive)
	}
	return nil
}

func applyDrivePullPlan(ctx context.Context, svc *drive.Service, rootPath string, plan driveSyncPlan) error {
	for _, action := range plan.Actions {
		if action.Type != "mkdir_local" {
			continue
		}
		dir := filepath.Join(rootPath, filepath.FromSlash(action.Path))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	for _, action := range plan.Actions {
		if action.Type != "download" {
			continue
		}
		dest := filepath.Join(rootPath, filepath.FromSlash(action.Path))
		if _, _, err := downloadDriveFile(ctx, svc, &drive.File{
			Id:       action.Drive,
			Name:     filepath.Base(dest),
			MimeType: action.MimeType,
		}, dest, ""); err != nil {
			return err
		}
	}
	for _, action := range plan.Actions {
		if action.Type != "delete_local" {
			continue
		}
		target := filepath.Join(rootPath, filepath.FromSlash(action.Path))
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func applyDrivePushPlan(ctx context.Context, svc *drive.Service, rootID string, rootPath string, remoteFolders map[string]driveTreeItem, plan driveSyncPlan) error {
	folderCache := map[string]string{"": rootID}
	for relPath, folder := range remoteFolders {
		if relPath == "" {
			continue
		}
		folderCache[relPath] = folder.ID
	}

	for _, action := range plan.Actions {
		if action.Type != "mkdir_drive" {
			continue
		}
		if _, err := ensureDriveFolder(ctx, svc, rootID, action.Path, folderCache); err != nil {
			return err
		}
	}
	for _, action := range plan.Actions {
		if action.Type != "upload" {
			continue
		}
		localPath := filepath.Join(rootPath, filepath.FromSlash(action.Path))
		dirPath := path.Dir(action.Path)
		parentID, err := ensureDriveFolder(ctx, svc, rootID, dirPath, folderCache)
		if err != nil {
			return err
		}

		f, err := os.Open(localPath) //nolint:gosec // user-provided path
		if err != nil {
			return err
		}
		defer f.Close()

		mimeType := guessMimeType(localPath)
		meta := &drive.File{Name: filepath.Base(localPath)}

		var created *drive.File
		if action.Drive != "" {
			created, err = svc.Files.Update(action.Drive, meta).
				SupportsAllDrives(true).
				Media(f, gapi.ContentType(mimeType)).
				Fields("id").
				Context(ctx).
				Do()
		} else {
			meta.Parents = []string{parentID}
			created, err = svc.Files.Create(meta).
				SupportsAllDrives(true).
				Media(f, gapi.ContentType(mimeType)).
				Fields("id").
				Context(ctx).
				Do()
		}
		if err != nil {
			return err
		}
		_ = created
	}
	for _, action := range plan.Actions {
		if action.Type != "delete_drive" {
			continue
		}
		if err := svc.Files.Delete(action.Drive).SupportsAllDrives(true).Context(ctx).Do(); err != nil {
			return err
		}
	}
	return nil
}

func ensureDriveFolder(ctx context.Context, svc *drive.Service, rootID string, dirPath string, cache map[string]string) (string, error) {
	dirPath = strings.TrimSpace(dirPath)
	if dirPath == "" || dirPath == "." || dirPath == "/" {
		return rootID, nil
	}
	if id, ok := cache[dirPath]; ok {
		return id, nil
	}

	parts := strings.Split(dirPath, "/")
	parentID := rootID
	curPath := ""
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		nextPath := part
		if curPath != "" {
			nextPath = curPath + "/" + part
		}
		if id, ok := cache[nextPath]; ok {
			parentID = id
			curPath = nextPath
			continue
		}

		folder := &drive.File{
			Name:     part,
			MimeType: driveMimeFolder,
			Parents:  []string{parentID},
		}
		created, err := svc.Files.Create(folder).
			SupportsAllDrives(true).
			Fields("id").
			Context(ctx).
			Do()
		if err != nil {
			return "", err
		}
		cache[nextPath] = created.Id
		parentID = created.Id
		curPath = nextPath
	}
	return parentID, nil
}
