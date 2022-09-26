package main

import (
	"html"
	"html/template"
	"log"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/state/store"
	"github.com/diamondburned/ningen/v3/discordmd"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	mdhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

var Replacer = strings.NewReplacer(
	"https://discord.com/channels/", "https://dfs.ioi-xd.net/",
)

type MessageGroup struct {
	Author
	Messages []Message
}

type Message struct {
	discord.Message
	Role             string
	MediaPreviews    []MediaPreview
	PlainAttachments []PlainAttachment
	Cabinet          *store.Cabinet
}

type Author struct {
	ID        discord.UserID
	Name      string
	Avatar    string
	Bot       bool
	Role      string
	RoleColor string
}

type MediaPreview struct {
	Thumbnail template.URL
	URL       template.URL
}

type PlainAttachment struct {
	Name string
	URL  template.URL
}

// message massages a discord.Message into a Message for passing to templates
func (s *server) message(m discord.Message) Message {
	msg := Message{
		Message: m,
		Cabinet: *&s.discord.Cabinet,
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
				template.URL(e.Thumbnail.URL),
				template.URL(url),
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
			template.URL(att.URL), template.URL(att.URL),
		})
	}
	msg.MediaPreviews = mediapreviews
	msg.PlainAttachments = plainatt
	return msg
}

func (m *Message) RenderMessageWithEmotes() template.HTML {
	return m.render(true)
}
func (m *Message) RenderMessageWithoutEmotes() template.HTML {
	return m.render(false)
}

func (s *server) author(m discord.Message) Author {
	auth := Author{
		ID:     m.Author.ID,
		Name:   m.Author.Username,
		Avatar: m.Author.AvatarURL(),
		Bot:    m.Author.Bot,
	}
	var role string
	var color string
	mr, err := s.discord.Member(m.GuildID, m.Author.ID)
	if err == nil {
		for _, rid := range mr.RoleIDs {
			rl, err := s.discord.Role(m.GuildID, rid)
			if err != nil {
				continue
			}
			if rl.Hoist {
				role = rl.Name
				color = rl.Color.String()
				break
			}
		}
	} else {
		log.Println("Failed to get a member: ", err)
	}
	auth.Role = role
	auth.RoleColor = color
	return auth
}

func (m *Message) render(renderEmotes bool) template.HTML {
	if m.Content != "" &&
		(len(m.Embeds) == 1 && m.Embeds[0].Type == discord.ImageEmbed && m.Embeds[0].URL == m.Content) {
		return ""
	}
	var sb strings.Builder
	src := []byte(m.Content)
	ast := discordmd.ParseWithMessage(src, *m.Cabinet, &m.Message, true)
	var r Renderer
	if renderEmotes {
		r = RendererWithEmotes{}
	} else {
		r = RendererNoEmotes{}
	}
	renderer := renderer.NewRenderer(
		renderer.WithNodeRenderers(
			util.Prioritized(mdhtml.NewRenderer(), 0),
			util.Prioritized(r, 0),
		),
	)
	renderer.Render(&sb, src, ast)
	return template.HTML(Replacer.Replace(sb.String()))
}

type Renderer interface{}
type RendererShared struct{}
type RendererWithEmotes struct {
	RendererShared
	Renderer
}
type RendererNoEmotes struct {
	RendererShared
	Renderer
}

func (r RendererWithEmotes) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(discordmd.KindMention, r.renderMentions)
	reg.Register(discordmd.KindEmoji, r.renderEmojis)
	reg.Register(discordmd.KindInline, r.renderMarkdown)
}
func (r RendererNoEmotes) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(discordmd.KindMention, r.renderMentions)
	reg.Register(discordmd.KindEmoji, r.renderEmojiNames)
	reg.Register(discordmd.KindInline, r.renderMarkdown)
}

func (r RendererShared) renderMentions(writer util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
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

func (r RendererShared) renderEmojis(writer util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		e, ok := n.(*discordmd.Emoji)
		if ok {
			writer.WriteString(`<img src='https://cdn.discordapp.com/emojis/` + e.ID + `.webp?size=40'></img>`)
		}
	}
	return ast.WalkContinue, nil
}

func (r RendererShared) renderEmojiNames(writer util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		e, ok := n.(*discordmd.Emoji)
		if ok {
			writer.WriteString(`<a class='brokenimage' href='https://cdn.discordapp.com/emojis/` + e.ID + `.webp?size=40'>:` + e.Name + `:</a>`)
		}
	}
	return ast.WalkContinue, nil
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

func (r RendererShared) renderMarkdown(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
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
