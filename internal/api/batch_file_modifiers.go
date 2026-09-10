package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

type batchAttachment struct {
	operation    int
	collectionID uuid.UUID
	field        string
	file         filestore.StagedFile
	modifier     fileModifier
	remove       []string
}

type fileModifier uint8

const (
	fileReplace fileModifier = iota
	fileAppend
	filePrepend
	fileRemove
)

type fileBatchParticipant struct {
	service     FileService
	principal   identity.Subject
	attachments []batchAttachment
}

func (p *fileBatchParticipant) CommitBatch(ctx context.Context, tx pgx.Tx, results []recordops.MutationResult) error {
	groups := map[string][]batchAttachment{}
	order := []string{}
	for _, item := range p.attachments {
		if item.operation >= len(results) || results[item.operation].Record == nil {
			return filestore.ErrInvalid
		}
		if results[item.operation].Replayed {
			continue
		}
		raw, ok := results[item.operation].Record.Data["id"].(string)
		if !ok {
			return filestore.ErrInvalid
		}
		recordID, err := uuid.Parse(raw)
		if err != nil {
			return filestore.ErrInvalid
		}
		key := strconv.Itoa(item.operation) + "/" + item.collectionID.String() + "/" + recordID.String() + "/" + item.field
		if _, present := groups[key]; !present {
			order = append(order, key)
		}
		groups[key] = append(groups[key], item)
	}
	for _, key := range order {
		items := groups[key]
		first := items[0]
		recordID := results[first.operation].Record.Data["id"].(string)
		parsedID, err := uuid.Parse(recordID)
		if err != nil {
			return filestore.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, first.collectionID.String()+"/"+parsedID.String()+"/"+first.field); err != nil {
			return err
		}
		rows, queryErr := tx.Query(ctx, `select id,filename from pgbase_meta.record_files where collection_id=$1 and record_id=$2 and field_name=$3 and status='ready' order by created_at,id`, first.collectionID, parsedID, first.field)
		if queryErr != nil {
			return queryErr
		}
		type state struct {
			id   uuid.UUID
			name string
			file *filestore.StagedFile
		}
		states := []state{}
		for rows.Next() {
			var item state
			if err := rows.Scan(&item.id, &item.name); err != nil {
				rows.Close()
				return err
			}
			states = append(states, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		existingIDs := make([]uuid.UUID, len(states))
		for index := range states {
			existingIDs[index] = states[index].id
		}
		for _, item := range items {
			addition := state{name: item.file.Filename(), file: &item.file}
			switch item.modifier {
			case fileReplace:
				states = []state{addition}
			case filePrepend:
				states = append([]state{addition}, states...)
			case fileAppend:
				states = append(states, addition)
			case fileRemove:
				remove := make(map[string]bool, len(item.remove))
				for _, name := range item.remove {
					remove[name] = true
				}
				kept := states[:0]
				for _, state := range states {
					if !remove[state.name] {
						kept = append(kept, state)
					}
				}
				states = kept
			}
		}
		keptIDs := map[uuid.UUID]bool{}
		change := filestore.AttachmentChange{CollectionID: first.collectionID, RecordID: parsedID, Field: first.field}
		for _, state := range states {
			if state.file != nil {
				change.Additions = append(change.Additions, *state.file)
			} else {
				keptIDs[state.id] = true
			}
		}
		for _, item := range existingIDs {
			if !keptIDs[item] {
				change.DeleteIDs = append(change.DeleteIDs, item)
			}
		}
		if _, err := p.service.Commit(ctx, tx, p.principal, []filestore.AttachmentChange{change}); err != nil {
			return err
		}
		for index, state := range states {
			id := state.id
			if state.file != nil {
				id = state.file.ID()
			}
			if _, err := tx.Exec(ctx, `update pgbase_meta.record_files set created_at=statement_timestamp()+($2::bigint * interval '1 microsecond') where id=$1 and status='ready'`, id, index); err != nil {
				return err
			}
		}
	}
	return nil
}

func extractBatchAttachments(ctx context.Context, service FileService, items []batchOperationInput, staged namedStagedFiles) ([]batchAttachment, error) {
	if service == nil {
		if len(staged) != 0 {
			return nil, errors.New("file service unavailable")
		}
		return []batchAttachment{}, nil
	}
	attachments := []batchAttachment{}
	used := map[string]bool{}
	for index := range items {
		collectionID, err := service.CollectionID(ctx, items[index].Collection)
		if err != nil {
			return nil, err
		}
		fields := make([]string, 0, len(items[index].Input))
		for field := range items[index].Input {
			fields = append(fields, field)
		}
		sort.Slice(fields, func(i, j int) bool {
			if len(fields[i]) != len(fields[j]) {
				return len(fields[i]) < len(fields[j])
			}
			return fields[i] < fields[j]
		})
		var fingerprint bytes.Buffer
		for _, field := range fields {
			raw := items[index].Input[field]
			base, modifier := splitFileModifier(field)
			isFile, err := service.IsFileField(ctx, collectionID, base)
			if err != nil {
				return nil, err
			}
			if !isFile {
				continue
			}
			if modifier == fileRemove {
				remove, ok := fileNames(raw)
				if !ok {
					return nil, errors.New("invalid file removal")
				}
				attachments = append(attachments, batchAttachment{operation: index, collectionID: collectionID, field: base, modifier: modifier, remove: remove})
				items[index].SidecarFields = append(items[index].SidecarFields, base)
				delete(items[index].Input, field)
				continue
			}
			var one struct {
				File string `json:"$file"`
			}
			if json.Unmarshal(raw, &one) == nil && one.File != "" {
				file, ok := staged[one.File]
				if !ok || used[one.File] {
					return nil, errors.New("invalid file reference")
				}
				used[one.File] = true
				appendFileFingerprint(&fingerprint, field, file)
				attachments = append(attachments, batchAttachment{operation: index, collectionID: collectionID, field: base, file: file, modifier: modifier})
				items[index].SidecarFields = append(items[index].SidecarFields, base)
				delete(items[index].Input, field)
				continue
			}
			var many []struct {
				File string `json:"$file"`
			}
			if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
				if modifier == filePrepend {
					slices.Reverse(many)
				}
				for fileIndex, ref := range many {
					file, ok := staged[ref.File]
					if ref.File == "" || !ok || used[ref.File] {
						return nil, errors.New("invalid file reference")
					}
					used[ref.File] = true
					appendFileFingerprint(&fingerprint, field, file)
					fileMode := modifier
					if modifier == fileReplace && fileIndex > 0 {
						fileMode = fileAppend
					}
					attachments = append(attachments, batchAttachment{operation: index, collectionID: collectionID, field: base, file: file, modifier: fileMode})
				}
				delete(items[index].Input, field)
				items[index].SidecarFields = append(items[index].SidecarFields, base)
			}
		}
		if fingerprint.Len() > 0 {
			digest := sha256.Sum256(fingerprint.Bytes())
			items[index].IdempotencyContext = append([]byte(nil), digest[:]...)
		}
	}
	if len(used) != len(staged) {
		return nil, errors.New("unreferenced file part")
	}
	return attachments, nil
}

func splitFileModifier(key string) (string, fileModifier) {
	if strings.HasPrefix(key, "+") && len(key) > 1 {
		return key[1:], filePrepend
	}
	if strings.HasSuffix(key, "+") && len(key) > 1 {
		return key[:len(key)-1], fileAppend
	}
	if strings.HasSuffix(key, "-") && len(key) > 1 {
		return key[:len(key)-1], fileRemove
	}
	return key, fileReplace
}

func fileNames(raw json.RawMessage) ([]string, bool) {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, one != ""
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil || many == nil {
		return nil, false
	}
	for _, name := range many {
		if name == "" {
			return nil, false
		}
	}
	return many, true
}

func appendFileFingerprint(buffer *bytes.Buffer, field string, file filestore.StagedFile) {
	digest := file.SHA256()
	buffer.WriteString(field)
	buffer.WriteByte(0)
	buffer.WriteString(file.Filename())
	buffer.WriteByte(0)
	buffer.WriteString(file.ContentType())
	buffer.WriteByte(0)
	buffer.WriteString(strconv.FormatInt(file.Size(), 10))
	buffer.WriteByte(0)
	buffer.Write(digest[:])
}
