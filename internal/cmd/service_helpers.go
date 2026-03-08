package cmd

import (
	"context"

	"google.golang.org/api/docs/v1"
	"google.golang.org/api/drive/v3"
)

func requireDocsService(ctx context.Context, flags *RootFlags) (*docs.Service, error) {
	account, err := requireAccount(flags)
	if err != nil {
		return nil, err
	}
	svc, err := newDocsService(ctx, account)
	if err != nil {
		return nil, err
	}
	return svc, nil
}

func requireDriveService(ctx context.Context, flags *RootFlags) (string, *drive.Service, error) {
	account, err := requireAccount(flags)
	if err != nil {
		return "", nil, err
	}
	svc, err := newDriveService(ctx, account)
	if err != nil {
		return "", nil, err
	}
	return account, svc, nil
}
