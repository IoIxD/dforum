package main

import (
	"fmt"
	"html"
	"html/template"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/ningen/v3/discordmd"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	mdhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

const (
	MaxThumbnailWidth  = 600
	MaxThumbnailHeight = 600
)

type MessageGroup struct {
	Author
	Messages []Message
}

type Message struct {
	discord.Message
	Role             string
	RenderedContent  template.HTML
	MediaPreviews    []MediaPreview
	PlainAttachments []PlainAttachment
}

type Author struct {
	ID        discord.UserID
	Name      string
	Avatar    string
	Bot       bool
	Role      string
	OtherRoles 	[]*discord.Role
	RoleColor string
}

type MediaPreview struct {
	Thumbnail   template.URL
	URL         template.URL
	Description string
}

type PlainAttachment struct {
	Name string
	URL  template.URL
}

func attachmentThumbnail(at discord.Attachment) template.URL {
	w, h := at.Width, at.Height
	if w > MaxThumbnailWidth {
		h = h * MaxThumbnailWidth / w
		w = MaxThumbnailWidth
	}
	if h > MaxThumbnailHeight {
		w = w * MaxThumbnailHeight / h
		h = MaxThumbnailHeight
	}

	const urlprefixlen = len("https://cdn.discordapp.com/")
	if len(at.URL) < urlprefixlen {
		return ""
	}
	return template.URL(fmt.Sprintf(
		"https://media.discordapp.net/%s?width=%d&height=%d",
		at.URL[urlprefixlen:], w, h,
	))
}

// message massages a discord.Message into a Message for passing to templates
func (s *server) message(m discord.Message) Message {
	msg := Message{
		Message:         m,
		RenderedContent: s.renderContent(m),
	}
	var mediapreviews []MediaPreview
	for _, e := range m.Embeds {
		if e.Thumbnail == nil {
			continue
		}
		var url string
		switch {
		case e.Video != nil:
			if e.Provider != nil {
				url = e.URL
			} else {
				url = e.Video.URL
			}
		case e.Image != nil:
			url = e.Image.Proxy
		default:
			url = e.Thumbnail.URL
		}
		mediapreviews = append(
			mediapreviews,
			MediaPreview{
				Thumbnail: template.URL(e.Thumbnail.URL),
				URL:       template.URL(url),
			},
		)
	}
	var plainatt []PlainAttachment
	for _, att := range m.Attachments {
		if att.Height == 0 ||
			!strings.HasPrefix(att.ContentType, "image/") {
			plainatt = append(plainatt, PlainAttachment{
				att.Filename,
				template.URL(att.URL),
			})
			continue
		}
		mediapreviews = append(mediapreviews, MediaPreview{
			Thumbnail:   attachmentThumbnail(att),
			URL:         template.URL(att.URL),
			Description: att.Description,
		})
	}
	msg.MediaPreviews = mediapreviews
	msg.PlainAttachments = plainatt
	return msg
}

func (s *server) author(m discord.Message) Author {
	auth := Author{
		ID:   m.Author.ID,
		Name: m.Author.Username,
		Bot:  m.Author.Bot,
	}
	var role string
	var color string
	mr, err := s.discord.Cabinet.Member(m.GuildID, m.Author.ID)
	if err != nil {
		// not a real error, just means the user is not in the guild
		m.Author.Avatar = ""
		auth.Avatar = m.Author.AvatarURLWithType(discord.WebPImage) + "?size=128"
		return auth
	}
	auth.Avatar = mr.User.AvatarURLWithType(discord.WebPImage) + "?size=128"
	auth.OtherRoles = make([]*discord.Role, 0)
	
	for _, rid := range mr.RoleIDs {
		rl, err := s.discord.Cabinet.Role(m.GuildID, rid)
		if err != nil {
			continue
		}
		auth.OtherRoles = append(auth.OtherRoles, rl)
		if rl.Hoist {
			role = rl.Name
			color = rl.Color.String()
		}
	}
	auth.Role = role
	auth.RoleColor = color
	return auth
}

func (s *server) renderContent(m discord.Message) template.HTML {
	if m.Content != "" &&
		(len(m.Embeds) == 1 && m.Embeds[0].Type == discord.ImageEmbed && m.Embeds[0].URL == m.Content) {
		return ""
	}
	var sb strings.Builder
	src := []byte(m.Content)
	ast := discordmd.ParseWithMessage(src, *s.discord.Cabinet, &m, true)
	renderer := renderer.NewRenderer(
		renderer.WithNodeRenderers(
			util.Prioritized(mdhtml.NewRenderer(), 0),
			util.Prioritized(mentionRenderer{}, 0),
			util.Prioritized(emoteRenderer{}, 0),
			util.Prioritized(inlineRenderer{}, 0),
		),
	)
	renderer.Render(&sb, src, ast)
	return template.HTML(strings.ReplaceAll(sb.String(), "https://discord.com/channels", s.URL))
}

type mentionRenderer struct{}

func (r mentionRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(discordmd.KindMention, r.render)
}
func (r mentionRenderer) render(writer util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		m := n.(*discordmd.Mention)
		switch {
		case m.Channel != nil:
			writer.WriteString("#")
			writer.WriteString(html.EscapeString(m.Channel.Name))
		case m.GuildUser != nil:
			writer.WriteString("@")
			writer.WriteString(html.EscapeString(m.GuildUser.Username))
		case m.GuildRole != nil:
			writer.WriteString("@")
			writer.WriteString(html.EscapeString(m.GuildRole.Name))
		default:
			writer.WriteString(string(source))
		}
	}
	return ast.WalkContinue, nil
}

type emoteRenderer struct{}

func (r emoteRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(discordmd.KindEmoji, r.render)
}
func (r emoteRenderer) render(writer util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		e, ok := n.(*discordmd.Emoji)
		if ok {
			writer.WriteString(`<img src='https://cdn.discordapp.com/emojis/` + e.ID + `.webp?size=40'></img>`)
		}
	}
	return ast.WalkContinue, nil
}

type inlineRenderer struct{}

func (r inlineRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(discordmd.KindInline, r.render)
}

var attrElements = []struct {
	Attr    discordmd.Attribute
	Element string
}{
	{discordmd.AttrBold, "strong"},
	{discordmd.AttrUnderline, "u"},
	{discordmd.AttrItalics, "em"},
	{discordmd.AttrStrikethrough, "del"},
	{discordmd.AttrMonospace, "code"},
}

func (r inlineRenderer) render(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	i := n.(*discordmd.Inline)
	if entering {
		for _, at := range attrElements {
			if i.Attr.Has(at.Attr) {
				w.WriteString("<")
				w.WriteString(at.Element)
				w.WriteString(">")
			}
		}
	} else {
		for _, at := range attrElements {
			if i.Attr.Has(at.Attr) {
				w.WriteString("</")
				w.WriteString(at.Element)
				w.WriteString(">")
			}
		}
	}
	return ast.WalkContinue, nil
}
