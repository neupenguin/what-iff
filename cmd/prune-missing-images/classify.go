package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/storage"
)

// verdict is what the object store says about an image row.
type verdict int

const (
	// verdictPresent means the image's bytes were found; foundKey says where.
	verdictPresent verdict = iota
	// verdictMissing means every place the image could live answered "not found". Only this
	// verdict is ever acted on by deleting.
	verdictMissing
	// verdictUnverified means a lookup failed for a reason other than "not found" (an outage, a
	// permissions problem), so nothing can be said about the image. It is never deleted.
	verdictUnverified
)

func (v verdict) String() string {
	switch v {
	case verdictPresent:
		return "present"
	case verdictMissing:
		return "missing"
	default:
		return "unverified"
	}
}

// downloadFunc is storage.FileStore.DownloadFile: (nil, nil) means the object does not exist, an
// error means the lookup itself failed.
type downloadFunc func(ctx context.Context, key string) ([]byte, error)

// candidateKeys lists the object keys an image row's bytes can be stored under, most likely first:
// the stored s3_key, the gallery key every image upload uses, then the chat and personality keys
// older paths used. Duplicates are dropped.
func candidateKeys(userID uuid.UUID, att *models.FileAttachment) []string {
	var keys []string
	seen := map[string]bool{}
	add := func(key string) {
		if key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	add(att.S3Key)
	add(storage.FileKeyForImage(userID, att.ID, att.Name))
	if att.ChatID != nil {
		add(storage.FileKeyForChat(userID, *att.ChatID, att.ID, att.Name))
	}
	if att.PersonalityID != nil {
		add(storage.FileKeyForPersonality(userID, *att.PersonalityID, att.ID, att.Name))
	}
	return keys
}

// classify looks for an image's bytes in the object store. It stops at the first key that has them
// (present). It is missing only when every key answered "not found"; if any lookup errored and none
// found the bytes, it is unverified, because the failed lookup might have been the right key.
func classify(ctx context.Context, download downloadFunc, userID uuid.UUID, att *models.FileAttachment) (v verdict, foundKey string, lookupErr error) {
	for _, key := range candidateKeys(userID, att) {
		data, err := download(ctx, key)
		if err != nil {
			lookupErr = fmt.Errorf("%s: %w", key, err)
			continue
		}
		if len(data) > 0 {
			return verdictPresent, key, nil
		}
	}
	if lookupErr != nil {
		return verdictUnverified, "", lookupErr
	}
	return verdictMissing, "", nil
}
