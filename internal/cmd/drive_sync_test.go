package cmd

import (
	"testing"
	"time"
)

func TestAllowSyncPath(t *testing.T) {
	includes := []string{"foo/*.txt"}
	excludes := []string{"foo/bad.txt"}

	if !allowSyncPath("foo/good.txt", includes, excludes) {
		t.Fatalf("expected allow for good.txt")
	}
	if allowSyncPath("foo/bad.txt", includes, excludes) {
		t.Fatalf("expected block for bad.txt")
	}
	if allowSyncPath("bar.txt", includes, excludes) {
		t.Fatalf("expected block for bar.txt")
	}
}

func TestNeedsPushSkipsGoogleDocs(t *testing.T) {
	remote := driveTreeItem{MimeType: driveMimeGoogleDoc}
	local := localFileInfo{}
	if needsPush(remote, local, false) {
		t.Fatalf("expected google doc to be skipped on push")
	}
}

func TestBuildDrivePullPlan(t *testing.T) {
	now := time.Now().UTC()
	remoteFiles := map[string]driveTreeItem{
		"a.txt": {ID: "1", Size: 10, ModifiedTime: now.Format(time.RFC3339)},
		"b.txt": {ID: "2", Size: 10, ModifiedTime: now.Format(time.RFC3339)},
	}
	localFiles := map[string]localFileInfo{
		"a.txt": {Size: 10, ModTime: now},
		"c.txt": {Size: 5, ModTime: now},
	}
	cfg := driveSyncConfig{}

	plan := buildDrivePullPlan(remoteFiles, nil, localFiles, cfg, true, false)
	if plan.Summary.Download != 1 {
		t.Fatalf("download count = %d, want 1", plan.Summary.Download)
	}
	if plan.Summary.DeleteLocal != 1 {
		t.Fatalf("delete_local count = %d, want 1", plan.Summary.DeleteLocal)
	}
}

func TestBuildDrivePushPlan(t *testing.T) {
	now := time.Now().UTC()
	remoteFiles := map[string]driveTreeItem{
		"a.txt": {ID: "1", Size: 5, ModifiedTime: now.Add(-time.Hour).Format(time.RFC3339)},
	}
	localFiles := map[string]localFileInfo{
		"a.txt": {Size: 10, ModTime: now},
	}
	cfg := driveSyncConfig{}

	plan := buildDrivePushPlan(remoteFiles, localFiles, cfg, false, false)
	if plan.Summary.Upload != 1 {
		t.Fatalf("upload count = %d, want 1", plan.Summary.Upload)
	}
}
