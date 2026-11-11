package imessage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const (
	defaultOpenWindow = 10
	messageRefPrefix  = archive.MessageRefPrefix
)

var (
	errForeignRef      = errors.New("ref is not from imessage")
	messageGUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	chatIDPattern      = regexp.MustCompile(`^(?:[0-9a-fA-F]{64}|[^;[:space:]]+;[+-];.+)$`)
)

func (c *Crawler) loadOpenMessage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (archive.MessageContext, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archive.MessageContext{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messageID, err := c.resolveOpenMessageID(ctx, req, localShortReference)
	if err != nil {
		return archive.MessageContext{}, err
	}
	result, err := st.OpenMessage(ctx, messageID, defaultOpenWindow)
	if errors.Is(err, archive.ErrMessageNotFound) {
		return archive.MessageContext{}, commandErr(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage))
	}
	if err != nil {
		return archive.MessageContext{}, err
	}
	return result, nil
}

func (c *Crawler) resolveOpenMessageID(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (messages.MessageGUID, error) {
	if !trawlkit.ValidShortRef(trawlkit.LocalTrawlerShortReferenceText(localShortReference)) {
		return "", commandErr(1, "invalid_ref", render.LinkIsNotValid())
	}
	resolved, err := req.ResolveShortReference(ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandErr(1, "unknown_short_ref", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr(1, "ambiguous_short_ref", render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounMessage))
	}
	if err != nil {
		return "", err
	}
	messageID, err := parseMessageRef(trawlkit.CanonicalArchiveRecordReferenceText(resolved[0]))
	if err != nil {
		return "", err
	}
	return messageID, nil
}

func parseMessageRef(ref string) (messages.MessageGUID, error) {
	messageGUID, found := strings.CutPrefix(ref, messageRefPrefix)
	if !found {
		return "", errForeignRef
	}
	if !messageGUIDPattern.MatchString(messageGUID) {
		return "", commandErr(1, "invalid_ref", render.LinkIsNotValid())
	}
	return messages.MessageGUID(strings.ToUpper(messageGUID)), nil
}

// ParseCanonicalRecordReferenceForOpen recognises the iMessage references
// accepted by the root open command.
func ParseCanonicalRecordReferenceForOpen(argument string) (*trawlkit.CanonicalArchiveRecordReference, error) {
	argument = strings.TrimSpace(argument)
	if strings.HasPrefix(argument, archive.MessageRefPrefix) {
		messageGUID, err := parseMessageRef(argument)
		if err != nil {
			return nil, err
		}
		return trawlkit.NewCanonicalArchiveRecordReference(archive.MessageRef(messageGUID)), nil
	}
	if chatID, found := strings.CutPrefix(argument, archive.ChatRefPrefix); found {
		if !chatIDPattern.MatchString(chatID) {
			return nil, commandErr(1, "invalid_ref", render.LinkIsNotValid())
		}
		return trawlkit.NewCanonicalArchiveRecordReference(argument), nil
	}
	return nil, nil
}
