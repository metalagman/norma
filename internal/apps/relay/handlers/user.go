package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/normahq/norma/internal/apps/relay/auth"
	relaytelegram "github.com/normahq/norma/internal/apps/relay/channel/telegram"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	relaysession "github.com/normahq/norma/internal/apps/relay/session"
	"github.com/tgbotkit/runtime/events"
	"github.com/tgbotkit/runtime/handlers"
	"go.uber.org/fx"
)

type userHandler struct {
	ownerStore        *auth.OwnerStore
	inviteStore       *auth.InviteStore
	collaboratorStore *auth.CollaboratorStore
	messenger         *messenger.Messenger
	channel           *relaytelegram.Adapter
	tgClient          tgClientGetter
	botUsername       string
}

type tgClientGetter interface {
	GetMeWithResponse(ctx context.Context) (interface{ GetResult() *meResult }, error)
}

type meResult struct {
	Username *string `json:"username"`
}

type userHandlerParams struct {
	fx.In

	OwnerStore        *auth.OwnerStore
	InviteStore       *auth.InviteStore
	CollaboratorStore *auth.CollaboratorStore
	Messenger         *messenger.Messenger
	Channel           *relaytelegram.Adapter
	TelegramClient    tgClientGetter `name:"relay_telegram_client"`
}

func NewUserHandler(params userHandlerParams) *userHandler {
	return &userHandler{
		ownerStore:        params.OwnerStore,
		inviteStore:       params.InviteStore,
		collaboratorStore: params.CollaboratorStore,
		messenger:         params.Messenger,
		channel:           params.Channel,
		tgClient:          params.TelegramClient,
	}
}

func (h *userHandler) getBotUsername(ctx context.Context) string {
	if h.botUsername != "" {
		return h.botUsername
	}
	if h.tgClient == nil {
		return ""
	}
	resp, err := h.tgClient.GetMeWithResponse(ctx)
	if err != nil || resp == nil || resp.GetResult() == nil || resp.GetResult().Username == nil {
		return ""
	}
	h.botUsername = *resp.GetResult().Username
	return h.botUsername
}

func (h *userHandler) Register(registry handlers.RegistryInterface) {
	registry.OnCommand(h.onCommand)
}

func (h *userHandler) onCommand(ctx context.Context, event *events.CommandEvent) error {
	commandCtx, ok := h.channel.CommandContextFromEvent(event)
	if !ok {
		return nil
	}

	// Route "user" subcommands
	return h.HandleUserCommand(ctx, commandCtx)
}

func (h *userHandler) HandleUserCommand(ctx context.Context, commandCtx relaytelegram.CommandContext) error {
	// Owner-only check
	if !h.ownerStore.HasOwner() || !h.ownerStore.IsOwner(commandCtx.UserID) {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Only the bot owner can manage collaborators."); err != nil {
			return err
		}
		return nil
	}

	parts := strings.Fields(commandCtx.Args)
	if len(parts) == 0 {
		return h.sendUsage(ctx, commandCtx.Locator)
	}

	subcommand := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.Join(parts[1:], " ")
	}

	switch subcommand {
	case "add":
		return h.onAdd(ctx, commandCtx)
	case "list":
		return h.onList(ctx, commandCtx)
	case "remove":
		return h.onRemove(ctx, commandCtx, args)
	default:
		return h.sendUsage(ctx, commandCtx.Locator)
	}
}

func (h *userHandler) sendUsage(ctx context.Context, locator relaysession.SessionLocator) error {
	usage := "Usage:\n" +
		"• /user add - Generate invite link\n" +
		"• /user list - Show collaborators and active invites\n" +
		"• /user remove <id> - Remove collaborator by ID\n"
	return h.channel.SendPlain(ctx, locator, usage)
}

func (h *userHandler) onAdd(ctx context.Context, commandCtx relaytelegram.CommandContext) error {
	ownerID := fmt.Sprintf("%d", commandCtx.UserID)

	token, _, err := h.inviteStore.CreateInvite(ctx, ownerID)
	if err != nil {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Failed to create invite. Please try again."); err != nil {
			return err
		}
		return nil
	}

	inviteLink := fmt.Sprintf("https://t.me/%s?start=%s", h.getBotUsername(ctx), token)
	message := fmt.Sprintf("Visit this link to become a bot collaborator:\n%s", inviteLink)

	if err := h.channel.SendPlain(ctx, commandCtx.Locator, message); err != nil {
		return err
	}
	return nil
}

func (h *userHandler) onList(ctx context.Context, commandCtx relaytelegram.CommandContext) error {
	var lines []string

	// List collaborators from SQL
	collaborators, err := h.collaboratorStore.ListCollaborators(ctx)
	if err != nil {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Failed to list collaborators. Please try again."); err != nil {
			return err
		}
		return nil
	}

	if len(collaborators) > 0 {
		lines = append(lines, fmt.Sprintf("Collaborators (%d):", len(collaborators)))
		for _, c := range collaborators {
			lines = append(lines, fmt.Sprintf("• @%s (ID: %s) - Added %s", c.Username, c.UserID, c.AddedAt.Format("2006-01-02")))
		}
	}

	// List active invites from KV
	invites, err := h.inviteStore.ListInvites(ctx)
	if err != nil {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Failed to list invites. Please try again."); err != nil {
			return err
		}
		return nil
	}

	if len(invites) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, fmt.Sprintf("Active Invites (%d):", len(invites)))
		for _, inv := range invites {
			ttl := time.Until(inv.ExpiresAt)
			if ttl < 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("• %s... (expires in %.0fh)", inv.CreatedBy[:8], ttl.Hours()))
		}
	}

	if len(lines) == 0 {
		lines = append(lines, "No collaborators or active invites.")
	}

	if err := h.channel.SendPlain(ctx, commandCtx.Locator, strings.Join(lines, "\n")); err != nil {
		return err
	}
	return nil
}

func (h *userHandler) onRemove(ctx context.Context, commandCtx relaytelegram.CommandContext, args string) error {
	userID := strings.TrimSpace(args)
	if userID == "" {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Usage: /user remove <id>"); err != nil {
			return err
		}
		return nil
	}

	// Validate it's a numeric string
	if _, err := strconv.ParseInt(userID, 10, 64); err != nil {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Invalid user ID. Use numeric Telegram user ID."); err != nil {
			return err
		}
		return nil
	}

	// Check if collaborator exists
	existing, found, err := h.collaboratorStore.GetCollaborator(ctx, userID)
	if err != nil || !found {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Collaborator not found."); err != nil {
			return err
		}
		return nil
	}

	// Remove
	if err := h.collaboratorStore.RemoveCollaborator(ctx, userID); err != nil {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Failed to remove collaborator. Please try again."); err != nil {
			return err
		}
		return nil
	}

	if err := h.channel.SendPlain(ctx, commandCtx.Locator, fmt.Sprintf("Removed @%s from collaborators.", existing.Username)); err != nil {
		return err
	}
	return nil
}
