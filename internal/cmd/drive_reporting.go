package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"google.golang.org/api/drive/v3"
	gapi "google.golang.org/api/googleapi"

	"github.com/steipete/gogcli/internal/outfmt"
	"github.com/steipete/gogcli/internal/ui"
)

const driveDefaultPageSize = 1000

type DriveTreeCmd struct {
	Parent string `name:"parent" help:"Folder ID to start from (default: root)"`
	Depth  int    `name:"depth" help:"Max depth (0 = unlimited)" default:"2"`
	Max    int    `name:"max" help:"Max items to return (0 = unlimited)" default:"0"`
}

func (c *DriveTreeCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)
	account, err := requireAccount(flags)
	if err != nil {
		return err
	}

	rootID := strings.TrimSpace(c.Parent)
	if rootID == "" {
		rootID = "root"
	}
	depth := c.Depth
	if depth < 0 {
		depth = 0
	}
	maxItems := c.Max
	if maxItems < 0 {
		maxItems = 0
	}

	svc, err := newDriveService(ctx, account)
	if err != nil {
		return err
	}

	items, truncated, err := listDriveTree(ctx, svc, driveTreeOptions{
		RootID:        rootID,
		MaxDepth:      depth,
		MaxItems:      maxItems,
		Fields:        driveTreeFields,
		IncludeFiles:  true,
		IncludeFolder: true,
	})
	if err != nil {
		return err
	}

	if outfmt.IsJSON(ctx) {
		return outfmt.WriteJSON(ctx, os.Stdout, map[string]any{
			"items":     items,
			"truncated": truncated,
		})
	}

	if len(items) == 0 {
		u.Err().Println("No files")
		return nil
	}

	w, flush := tableWriter(ctx)
	defer flush()
	fmt.Fprintln(w, "PATH\tTYPE\tSIZE\tMODIFIED\tID")
	for _, it := range items {
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\n",
			sanitizeTab(it.Path),
			driveType(it.MimeType),
			formatDriveSize(it.Size),
			formatDateTime(it.ModifiedTime),
			it.ID,
		)
	}
	if truncated {
		u.Err().Println("Results truncated; increase --max to see more.")
	}
	return nil
}

type DriveInventoryCmd struct {
	Parent string `name:"parent" help:"Folder ID to start from (default: root)"`
	Depth  int    `name:"depth" help:"Max depth (0 = unlimited)" default:"0"`
	Max    int    `name:"max" help:"Max items to return (0 = unlimited)" default:"500"`
	Sort   string `name:"sort" help:"Sort by path|size|modified" default:"path"`
	Order  string `name:"order" help:"Sort order: asc|desc" default:"asc"`
}

func (c *DriveInventoryCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)
	account, err := requireAccount(flags)
	if err != nil {
		return err
	}

	rootID := strings.TrimSpace(c.Parent)
	if rootID == "" {
		rootID = "root"
	}
	depth := c.Depth
	if depth < 0 {
		depth = 0
	}
	maxItems := c.Max
	if maxItems < 0 {
		maxItems = 0
	}

	svc, err := newDriveService(ctx, account)
	if err != nil {
		return err
	}

	items, truncated, err := listDriveTree(ctx, svc, driveTreeOptions{
		RootID:        rootID,
		MaxDepth:      depth,
		MaxItems:      maxItems,
		Fields:        driveInventoryFields,
		IncludeFiles:  true,
		IncludeFolder: true,
	})
	if err != nil {
		return err
	}

	sortDriveInventory(items, c.Sort, c.Order)

	if outfmt.IsJSON(ctx) {
		return outfmt.WriteJSON(ctx, os.Stdout, map[string]any{
			"items":     items,
			"truncated": truncated,
		})
	}

	if len(items) == 0 {
		u.Err().Println("No files")
		return nil
	}

	w, flush := tableWriter(ctx)
	defer flush()
	fmt.Fprintln(w, "PATH\tTYPE\tSIZE\tMODIFIED\tOWNER\tID")
	for _, it := range items {
		owner := "-"
		if len(it.Owners) > 0 {
			owner = it.Owners[0]
		}
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			sanitizeTab(it.Path),
			driveType(it.MimeType),
			formatDriveSize(it.Size),
			formatDateTime(it.ModifiedTime),
			owner,
			it.ID,
		)
	}
	if truncated {
		u.Err().Println("Results truncated; increase --max to see more.")
	}
	return nil
}

type DriveDuCmd struct {
	Parent string `name:"parent" help:"Folder ID to start from (default: root)"`
	Depth  int    `name:"depth" help:"Depth for folder totals" default:"1"`
	Max    int    `name:"max" help:"Max folders to return (0 = unlimited)" default:"50"`
	Sort   string `name:"sort" help:"Sort by size|path|files" default:"size"`
	Order  string `name:"order" help:"Sort order: asc|desc" default:"desc"`
}

func (c *DriveDuCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)
	account, err := requireAccount(flags)
	if err != nil {
		return err
	}

	rootID := strings.TrimSpace(c.Parent)
	if rootID == "" {
		rootID = "root"
	}
	depth := c.Depth
	if depth < 0 {
		depth = 0
	}
	maxItems := c.Max
	if maxItems < 0 {
		maxItems = 0
	}

	svc, err := newDriveService(ctx, account)
	if err != nil {
		return err
	}

	items, truncated, err := listDriveTree(ctx, svc, driveTreeOptions{
		RootID:        rootID,
		MaxDepth:      0,
		MaxItems:      0,
		Fields:        driveTreeFields,
		IncludeFiles:  true,
		IncludeFolder: true,
	})
	if err != nil {
		return err
	}
	if truncated {
		return fmt.Errorf("drive du truncated unexpectedly")
	}

	summaries := summarizeDriveDu(items, rootID, depth)
	sortDriveDu(summaries, c.Sort, c.Order)

	if maxItems > 0 && len(summaries) > maxItems {
		summaries = summaries[:maxItems]
	}

	if outfmt.IsJSON(ctx) {
		return outfmt.WriteJSON(ctx, os.Stdout, map[string]any{
			"folders": summaries,
		})
	}

	if len(summaries) == 0 {
		u.Err().Println("No folders")
		return nil
	}

	w, flush := tableWriter(ctx)
	defer flush()
	fmt.Fprintln(w, "PATH\tSIZE\tFILES")
	for _, f := range summaries {
		fmt.Fprintf(w, "%s\t%s\t%d\n", sanitizeTab(f.Path), formatDriveSize(f.Size), f.Files)
	}
	return nil
}

type driveTreeItem struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	ParentID     string   `json:"parentId,omitempty"`
	MimeType     string   `json:"mimeType"`
	Size         int64    `json:"size,omitempty"`
	ModifiedTime string   `json:"modifiedTime,omitempty"`
	Owners       []string `json:"owners,omitempty"`
	MD5          string   `json:"md5,omitempty"`
	Depth        int      `json:"depth"`
}

func (d driveTreeItem) IsFolder() bool {
	return d.MimeType == driveMimeFolder
}

type driveTreeOptions struct {
	RootID        string
	MaxDepth      int
	MaxItems      int
	Fields        string
	IncludeFiles  bool
	IncludeFolder bool
}

type driveFolderQueueItem struct {
	ID    string
	Path  string
	Depth int
}

const (
	driveTreeFields      = "id,name,mimeType,size,modifiedTime"
	driveInventoryFields = "id,name,mimeType,size,modifiedTime,owners(emailAddress,displayName)"
)

func listDriveTree(ctx context.Context, svc *drive.Service, opts driveTreeOptions) ([]driveTreeItem, bool, error) {
	rootID := strings.TrimSpace(opts.RootID)
	if rootID == "" {
		rootID = "root"
	}
	fields := strings.TrimSpace(opts.Fields)
	if fields == "" {
		fields = driveTreeFields
	}

	queue := []driveFolderQueueItem{{ID: rootID, Path: "", Depth: 0}}
	out := make([]driveTreeItem, 0, 128)
	truncated := false

	for len(queue) > 0 {
		folder := queue[0]
		queue = queue[1:]

		children, err := listDriveChildren(ctx, svc, folder.ID, fields)
		if err != nil {
			return nil, false, err
		}
		for _, child := range children {
			if child == nil {
				continue
			}
			depth := folder.Depth + 1
			item := driveTreeItem{
				ID:           child.Id,
				Name:         child.Name,
				Path:         joinDrivePath(folder.Path, child.Name),
				ParentID:     folder.ID,
				MimeType:     child.MimeType,
				Size:         child.Size,
				ModifiedTime: child.ModifiedTime,
				Owners:       driveOwners(child),
				MD5:          child.Md5Checksum,
				Depth:        depth,
			}

			if item.IsFolder() {
				if opts.IncludeFolder {
					out = append(out, item)
				}
				if opts.MaxDepth <= 0 || depth < opts.MaxDepth {
					queue = append(queue, driveFolderQueueItem{ID: child.Id, Path: item.Path, Depth: depth})
				}
			} else if opts.IncludeFiles {
				out = append(out, item)
			}

			if opts.MaxItems > 0 && len(out) >= opts.MaxItems {
				truncated = true
				return out, truncated, nil
			}
		}
	}

	return out, truncated, nil
}

func listDriveChildren(ctx context.Context, svc *drive.Service, parentID string, fields string) ([]*drive.File, error) {
	if parentID == "" {
		parentID = "root"
	}
	q := buildDriveListQuery(parentID, "")
	out := make([]*drive.File, 0, 64)
	var pageToken string

	for {
		call := svc.Files.List().
			Q(q).
			PageSize(driveDefaultPageSize).
			PageToken(pageToken).
			OrderBy("folder,name").
			SupportsAllDrives(true).
			IncludeItemsFromAllDrives(true).
			Fields(
				gapi.Field("nextPageToken"),
				gapi.Field("files("+fields+")"),
			).
			Context(ctx)
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Files...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	return out, nil
}

func joinDrivePath(parent string, name string) string {
	name = sanitizeDriveName(name)
	if parent == "" {
		return name
	}
	return path.Join(parent, name)
}

func sanitizeDriveName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "_"
	}
	return name
}

func driveOwners(f *drive.File) []string {
	if f == nil || len(f.Owners) == 0 {
		return nil
	}
	out := make([]string, 0, len(f.Owners))
	for _, owner := range f.Owners {
		if owner == nil {
			continue
		}
		if owner.EmailAddress != "" {
			out = append(out, owner.EmailAddress)
		} else if owner.DisplayName != "" {
			out = append(out, owner.DisplayName)
		}
	}
	return out
}

type driveDuSummary struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Files int    `json:"files"`
	Depth int    `json:"depth"`
}

func summarizeDriveDu(items []driveTreeItem, rootID string, depthLimit int) []driveDuSummary {
	type folderMeta struct {
		path  string
		depth int
	}

	parentByID := map[string]string{}
	folderMetaByID := map[string]folderMeta{
		rootID: {path: ".", depth: 0},
	}
	for _, it := range items {
		if it.IsFolder() {
			parentByID[it.ID] = it.ParentID
			folderMetaByID[it.ID] = folderMeta{path: it.Path, depth: it.Depth}
		}
	}

	sizes := map[string]*driveDuSummary{}
	getSummary := func(id string) *driveDuSummary {
		if s, ok := sizes[id]; ok {
			return s
		}
		meta := folderMetaByID[id]
		s := &driveDuSummary{
			ID:    id,
			Path:  meta.path,
			Depth: meta.depth,
		}
		sizes[id] = s
		return s
	}

	for _, it := range items {
		if it.IsFolder() {
			continue
		}
		parentID := it.ParentID
		for parentID != "" {
			s := getSummary(parentID)
			s.Size += it.Size
			s.Files++
			parentID = parentByID[parentID]
		}
	}

	out := make([]driveDuSummary, 0, len(sizes))
	for _, s := range sizes {
		if depthLimit > 0 && s.Depth > depthLimit {
			continue
		}
		out = append(out, *s)
	}
	return out
}

func sortDriveDu(items []driveDuSummary, sortBy string, order string) {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	order = strings.ToLower(strings.TrimSpace(order))
	desc := order == "desc"

	less := func(i, j int) bool { return false }
	switch sortBy {
	case "path":
		less = func(i, j int) bool { return items[i].Path < items[j].Path }
	case "files":
		less = func(i, j int) bool { return items[i].Files < items[j].Files }
	default:
		less = func(i, j int) bool { return items[i].Size < items[j].Size }
	}

	sort.Slice(items, func(i, j int) bool {
		if desc {
			return !less(i, j)
		}
		return less(i, j)
	})
}

func sortDriveInventory(items []driveTreeItem, sortBy string, order string) {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	order = strings.ToLower(strings.TrimSpace(order))
	desc := order == "desc"

	less := func(i, j int) bool { return false }
	switch sortBy {
	case "size":
		less = func(i, j int) bool { return items[i].Size < items[j].Size }
	case "modified":
		less = func(i, j int) bool { return items[i].ModifiedTime < items[j].ModifiedTime }
	default:
		less = func(i, j int) bool { return items[i].Path < items[j].Path }
	}

	sort.Slice(items, func(i, j int) bool {
		if desc {
			return !less(i, j)
		}
		return less(i, j)
	})
}
