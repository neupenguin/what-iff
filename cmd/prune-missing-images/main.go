// Command prune-missing-images cleans up legacy image rows that have no s3_key: it looks for each
// one's bytes in the object store, fills in the s3_key of the ones it finds, and deletes the rows
// whose bytes are not there at all. Those rows predate the move of every file into S3; an image
// that is in neither place can no longer be shown or sent to a model, so the row is only debris.
//
// Safe by default: it prints what it found and changes nothing unless -yes is passed, it refuses
// to run against a production-looking ENV unless -force is also given, and it only ever deletes a
// row when the store positively answered "not found" for every place the image could live. A
// lookup that failed for any other reason leaves the row alone. It also refuses to change anything
// unless a known-good image (one with an s3_key) can be read from the same store, so a store that
// is misconfigured (say, the local-directory fallback pointed at an empty folder) cannot make every
// image look missing.
//
// Usage:
//
//	go run ./cmd/prune-missing-images                          # dry run, every user
//	go run ./cmd/prune-missing-images -email you@example.com   # dry run, one user
//	go run ./cmd/prune-missing-images -yes                     # backfill s3_key and delete
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"go.uber.org/zap"

	entfileattachment "github.com/theimaginaryfoundation/what-iff/ent/fileattachment"
	entuser "github.com/theimaginaryfoundation/what-iff/ent/user"
	"github.com/theimaginaryfoundation/what-iff/internal/database"
	"github.com/theimaginaryfoundation/what-iff/internal/datastore"
	"github.com/theimaginaryfoundation/what-iff/internal/logging"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/storage"
)

// probeRows is how many known-good images are tried to prove the store is the right one.
const probeRows = 5

// row is a legacy image row with what the store said about it.
type row struct {
	att      *models.FileAttachment
	owner    uuid.UUID
	verdict  verdict
	foundKey string
	err      error
}

func main() {
	var (
		email               string
		envFile             string
		confirm             bool
		force               bool
		insecureLocalSecret bool
	)
	flag.StringVar(&email, "email", "", "only look at this account's images (default: every account)")
	flag.StringVar(&envFile, "env", ".env", "env file path")
	flag.BoolVar(&confirm, "yes", false, "actually backfill and delete (omit for a dry run)")
	flag.BoolVar(&force, "force", false, "allow running against a production-looking ENV")
	flag.BoolVar(&insecureLocalSecret, "allow-insecure-local-secret", false, "allow a dummy token secret only for local development")
	flag.Parse()

	if err := godotenv.Load(envFile); err != nil {
		log.Printf("warning: could not load %s: %v", envFile, err)
	}
	env := strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))
	if env == "" {
		env = strings.ToLower(strings.TrimSpace(os.Getenv("ENVIRONMENT")))
	}
	if (env == "prod" || env == "production") && !force {
		log.Fatalf("refusing to run against ENV=%q without -force", env)
	}

	logger, err := logging.NewLogger()
	if err != nil {
		log.Fatalf("logger: %v", err)
	}
	defer logger.Sync()

	client, sqlDB, err := database.NewClient(logger)
	if err != nil {
		logger.Fatal("connect db", zap.Error(err))
	}
	defer client.Close()
	defer sqlDB.Close()

	// The token secret is required to construct the datastore but this tool never touches
	// encrypted tokens, so a dummy is allowed only with an explicit local-development escape hatch.
	secret := os.Getenv("TOKEN_ENCRYPTION_SECRET")
	if len(secret) < datastore.MinTokenEncryptionSecretLen {
		if !insecureLocalSecret || (env != "development" && env != "local" && env != "test") {
			logger.Fatal("TOKEN_ENCRYPTION_SECRET is required; use -allow-insecure-local-secret only in local development")
		}
		secret = strings.Repeat("0", datastore.MinTokenEncryptionSecretLen)
	}
	ds, err := datastore.NewDatastore(client, sqlDB, logger, secret, nil)
	if err != nil {
		logger.Fatal("init datastore", zap.Error(err))
	}

	ctx := context.Background()
	bucket := os.Getenv("S3_FILE_BUCKET")
	store, err := storage.NewFileStore(ctx, bucket, os.Getenv("AWS_REGION"), logger)
	if err != nil {
		logger.Fatal("init file store", zap.Error(err))
	}
	if bucket == "" {
		fmt.Println("NOTE: S3_FILE_BUCKET is not set, so this reads the LOCAL file store, not S3.")
	} else {
		fmt.Printf("Object store: S3 bucket %q\n", bucket)
	}

	// Legacy image rows: an image with no s3_key.
	query := client.FileAttachment.Query().
		Where(
			entfileattachment.FileTypeHasPrefix(models.ImageMIMEPrefix),
			entfileattachment.Or(entfileattachment.S3KeyIsNil(), entfileattachment.S3KeyEQ("")),
		).
		WithOwner()
	probeQuery := client.FileAttachment.Query().
		Where(
			entfileattachment.FileTypeHasPrefix(models.ImageMIMEPrefix),
			entfileattachment.S3KeyNotNil(), entfileattachment.S3KeyNEQ(""),
		).
		Limit(probeRows)
	if email = strings.TrimSpace(email); email != "" {
		who := entfileattachment.HasOwnerWith(entuser.EmailEQ(email))
		query = query.Where(who)
		probeQuery = probeQuery.Where(who)
	}
	entRows, err := query.All(ctx)
	if err != nil {
		logger.Fatal("list legacy images", zap.Error(err))
	}
	fmt.Printf("Legacy image rows without an s3_key: %d\n", len(entRows))
	if len(entRows) == 0 {
		return
	}

	knownKeys, err := probeQuery.Select(entfileattachment.FieldS3Key).Strings(ctx)
	if err != nil {
		logger.Fatal("list known images", zap.Error(err))
	}
	probeOK := probeStore(ctx, store, knownKeys)

	rows := make([]row, 0, len(entRows))
	for _, e := range entRows {
		if e.Edges.Owner == nil {
			logger.Warn("image row has no owner; skipping", zap.String("id", e.ID.String()))
			continue
		}
		owner := e.Edges.Owner.ID
		att, err := ds.GetFileAttachment(ctx, owner, e.ID)
		if err != nil {
			logger.Warn("could not load image row; skipping", zap.String("id", e.ID.String()), zap.Error(err))
			continue
		}
		v, key, lookupErr := classify(ctx, store.DownloadFile, owner, att)
		rows = append(rows, row{att: att, owner: owner, verdict: v, foundKey: key, err: lookupErr})
	}
	present, missing, unverified := split(rows)

	fmt.Printf("  bytes found in the store (s3_key can be filled in): %d\n", len(present))
	fmt.Printf("  not in the store at all (rows can be deleted):      %d\n", len(missing))
	fmt.Printf("  could not be checked (left alone):                  %d\n", len(unverified))
	printRows("Missing from the store", missing)
	printRows("Could not be checked", unverified)

	if !confirm {
		fmt.Println("\nDry run — re-run with -yes to apply.")
		return
	}
	if !probeOK {
		logger.Fatal("refusing to change anything: could not read a known-good image from this store, so a \"not found\" here may mean the wrong store rather than a missing image")
	}

	filled := 0
	for _, r := range present {
		if err := ds.SetFileAttachmentS3Key(ctx, r.owner, r.att.ID, r.foundKey); err != nil {
			logger.Warn("could not fill in s3_key", zap.String("id", r.att.ID.String()), zap.Error(err))
			continue
		}
		filled++
	}
	deleted := 0
	for _, r := range missing {
		if err := ds.DeleteFileAttachment(ctx, r.owner, r.att.ID); err != nil {
			logger.Warn("could not delete image row", zap.String("id", r.att.ID.String()), zap.Error(err))
			continue
		}
		deleted++
	}
	fmt.Printf("\nDone: filled in %d s3_key(s), deleted %d row(s).\n", filled, deleted)
}

// probeStore reports whether at least one image that is known to be in the store (it has an
// s3_key) can be read back, i.e. whether this is the store those rows live in.
func probeStore(ctx context.Context, store storage.FileStore, known []string) bool {
	if len(known) == 0 {
		fmt.Println("Store check: there is no image with an s3_key to test the store against.")
		return false
	}
	for _, key := range known {
		if data, err := store.DownloadFile(ctx, key); err == nil && len(data) > 0 {
			fmt.Printf("Store check: read a known image (%d bytes), so this is the right store.\n", len(data))
			return true
		}
	}
	fmt.Printf("Store check: could not read any of %d known images — wrong store or no access.\n", len(known))
	return false
}

// split groups rows by verdict, keeping order.
func split(rows []row) (present, missing, unverified []row) {
	for _, r := range rows {
		switch r.verdict {
		case verdictPresent:
			present = append(present, r)
		case verdictMissing:
			missing = append(missing, r)
		default:
			unverified = append(unverified, r)
		}
	}
	return present, missing, unverified
}

func printRows(title string, rows []row) {
	if len(rows) == 0 {
		return
	}
	fmt.Printf("\n%s:\n", title)
	for _, r := range rows {
		line := fmt.Sprintf("  %s  owner %s  %q  created %s", r.att.ID, r.owner, r.att.Name, r.att.CreatedAt.Format(time.DateOnly))
		if r.err != nil {
			line += "  (" + r.err.Error() + ")"
		}
		fmt.Println(line)
	}
}
