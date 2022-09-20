package main

import (
	"html"
	"html/template"
	"regexp"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/ningen/v3/discordmd"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	mdhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

var Replacer = strings.NewReplacer(
	"\n", "<br>",
	"https://discord.com/channels/", "https://dfs.ioi-xd.net/",
)

var re = regexp.MustCompile(`http(s|)://discord.com/channels/(.*?)/`)

type Message struct {
	discord.Message
	RenderedContent  template.HTML
	MediaPreviews    []MediaPreview
	PlainAttachments []PlainAttachment
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
			util.Prioritized(inlineRenderer{}, 0),
		),
	)
	renderer.Render(&sb, src, ast)
	str := Replacer.Replace(sb.String())
	str = string(re.ReplaceAll([]byte(str), []byte("<a href='http$1://dfs.ioi-xd.net/$2'>http$1://discordapp.com/$2</a>")))
	return template.HTML(str)
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
