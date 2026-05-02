package mcp

import (
	"context"
	"encoding/json"
	"errors"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lnyarl/discordlogbot/internal/auth"
	"github.com/lnyarl/discordlogbot/internal/permissions"
)

// ChannelLister is the data dependency of the list_channels tool. The
// indirection lets tests inject a stub instead of hitting Discord.
type ChannelLister interface {
	ListChannels(ctx context.Context, userID string) ([]permissions.AccessibleChannel, error)
}

// DiscordLister adapts permissions.ComputeAccessibleChannels into the
// ChannelLister interface for production use.
type DiscordLister struct {
	Client *permissions.Client
}

func NewDiscordLister(c *permissions.Client) *DiscordLister {
	return &DiscordLister{Client: c}
}

func (d *DiscordLister) ListChannels(ctx context.Context, userID string) ([]permissions.AccessibleChannel, error) {
	return permissions.ComputeAccessibleChannels(ctx, d.Client, userID)
}

// Server bundles an MCP SDK server pre-loaded with our tools. The exposed
// SDK() is what the SSE handler hands to the SDK's getServer callback.
type Server struct {
	sdk    *mcpsdk.Server
	lister ChannelLister
}

// listChannelsArgs has no fields; the SDK still requires a struct type
// for the generic AddTool call so it can synthesise an input schema.
type listChannelsArgs struct{}

func NewServer(lister ChannelLister) *Server {
	sdk := mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "discord-logbot", Version: "0.1.0"},
		nil,
	)
	s := &Server{sdk: sdk, lister: lister}

	mcpsdk.AddTool(sdk, &mcpsdk.Tool{
		Name:        "list_channels",
		Description: "Return the Discord channels accessible to the authenticated user.",
	}, s.handleListChannels)

	return s
}

func (s *Server) SDK() *mcpsdk.Server { return s.sdk }

func (s *Server) handleListChannels(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	_ listChannelsArgs,
) (*mcpsdk.CallToolResult, any, error) {
	userID, ok := auth.UserIDFrom(ctx)
	if !ok {
		return nil, nil, errors.New("missing user_id in context")
	}
	channels, err := s.lister.ListChannels(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if channels == nil {
		channels = []permissions.AccessibleChannel{}
	}
	body, err := json.MarshalIndent(channels, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
	}, nil, nil
}
