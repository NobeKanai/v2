// Copyright 2017 Frédéric Guillot. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package sanitizer // import "miniflux.app/reader/sanitizer"

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"miniflux.app/config"
	"miniflux.app/url"

	"golang.org/x/net/html"
)

var (
	youtubeEmbedRegex = regexp.MustCompile(`//www\.youtube\.com/embed/(.*)`)
)

// Sanitize returns safe HTML.
func Sanitize(baseURL, input string) string {
	var buffer bytes.Buffer
	var tagStack []string
	var parentTag string
	blacklistedTagDepth := 0

	tokenizer := html.NewTokenizer(bytes.NewBufferString(input))
	for {
		if tokenizer.Next() == html.ErrorToken {
			err := tokenizer.Err()
			if err == io.EOF {
				return buffer.String()
			}

			return ""
		}

		token := tokenizer.Token()
		switch token.Type {
		case html.TextToken:
			if blacklistedTagDepth > 0 {
				continue
			}

			// An iframe element never has fallback content.
			// See https://www.w3.org/TR/2010/WD-html5-20101019/the-iframe-element.html#the-iframe-element
			if parentTag == "iframe" {
				continue
			}

			buffer.WriteString(html.EscapeString(token.Data))
		case html.StartTagToken:
			tagName := token.DataAtom.String()
			parentTag = tagName

			if !isPixelTracker(tagName, token.Attr) && isValidTag(tagName) {
				attrNames, htmlAttributes := sanitizeAttributes(baseURL, tagName, token.Attr)

				if hasRequiredAttributes(tagName, attrNames) {
					if len(attrNames) > 0 {
						buffer.WriteString("<" + tagName + " " + htmlAttributes + ">")
					} else {
						buffer.WriteString("<" + tagName + ">")
					}

					tagStack = append(tagStack, tagName)
				}
			} else if isBlockedTag(tagName) {
				blacklistedTagDepth++
			}
		case html.EndTagToken:
			tagName := token.DataAtom.String()
			if isValidTag(tagName) && inList(tagName, tagStack) {
				buffer.WriteString(fmt.Sprintf("</%s>", tagName))
			} else if isBlockedTag(tagName) {
				blacklistedTagDepth--
			}
		case html.SelfClosingTagToken:
			tagName := token.DataAtom.String()
			if !isPixelTracker(tagName, token.Attr) && isValidTag(tagName) {
				attrNames, htmlAttributes := sanitizeAttributes(baseURL, tagName, token.Attr)

				if hasRequiredAttributes(tagName, attrNames) {
					if len(attrNames) > 0 {
						buffer.WriteString("<" + tagName + " " + htmlAttributes + "/>")
					} else {
						buffer.WriteString("<" + tagName + "/>")
					}
				}
			}
		}
	}
}

func sanitizeAttributes(baseURL, tagName string, attributes []html.Attribute) ([]string, string) {
	var htmlAttrs, attrNames []string
	var err error
	var isImageLargerThanLayout bool
	var isAnchorLink bool

	if tagName == "img" {
		imgWidth := getIntegerAttributeValue("width", attributes)
		isImageLargerThanLayout = imgWidth > 750
	}

	for _, attribute := range attributes {
		value := attribute.Val

		if !isValidAttribute(tagName, attribute.Key) {
			continue
		}

		if (tagName == "img" || tagName == "source") && attribute.Key == "srcset" {
			value = sanitizeSrcsetAttr(baseURL, value)
		}

		if tagName == "img" && (attribute.Key == "width" || attribute.Key == "height") {
			if !isPositiveInteger(value) {
				continue
			}

			if isImageLargerThanLayout {
				continue
			}
		}

		if isExternalResourceAttribute(attribute.Key) {
			if tagName == "iframe" {
				if isValidIframeSource(baseURL, attribute.Val) {
					value = rewriteIframeURL(attribute.Val)
				} else {
					continue
				}
			} else if tagName == "img" && attribute.Key == "src" && isValidDataAttribute(attribute.Val) {
				value = attribute.Val
			} else if isAnchor("a", attribute) {
				value = attribute.Val
				isAnchorLink = true
			} else {
				value, err = url.AbsoluteURL(baseURL, value)
				if err != nil {
					continue
				}

				if !hasValidURIScheme(value) || isBlockedResource(value) {
					continue
				}
			}
		}

		attrNames = append(attrNames, attribute.Key)
		htmlAttrs = append(htmlAttrs, fmt.Sprintf(`%s="%s"`, attribute.Key, html.EscapeString(value)))
	}

	if !isAnchorLink {
		extraAttrNames, extraHTMLAttributes := getExtraAttributes(tagName)
		if len(extraAttrNames) > 0 {
			attrNames = append(attrNames, extraAttrNames...)
			htmlAttrs = append(htmlAttrs, extraHTMLAttributes...)
		}
	}

	return attrNames, strings.Join(htmlAttrs, " ")
}

func getExtraAttributes(tagName string) ([]string, []string) {
	switch tagName {
	case "a":
		return []string{"rel", "target", "referrerpolicy"}, []string{`rel="noopener noreferrer"`, `target="_blank"`, `referrerpolicy="no-referrer"`}
	case "video", "audio":
		return []string{"controls"}, []string{"controls"}
	case "iframe":
		return []string{"sandbox", "loading"}, []string{`sandbox="allow-scripts allow-same-origin allow-popups"`, `loading="lazy"`}
	case "img":
		return []string{"loading"}, []string{`loading="lazy"`}
	default:
		return nil, nil
	}
}

func isValidTag(tagName string) bool {
	_, ok := tagAllowList[tagName]
	return ok
}

func isValidAttribute(tagName, attributeName string) bool {
	if attributes, ok := tagAllowList[tagName]; ok {
		if inList(attributeName, attributes) {
			return true
		}
	}

	return false
}

func isExternalResourceAttribute(attribute string) bool {
	switch attribute {
	case "src", "href", "poster", "cite":
		return true
	default:
		return false
	}
}

func isPixelTracker(tagName string, attributes []html.Attribute) bool {
	if tagName == "img" {
		hasHeight := false
		hasWidth := false

		for _, attribute := range attributes {
			if attribute.Key == "height" && attribute.Val == "1" {
				hasHeight = true
			}

			if attribute.Key == "width" && attribute.Val == "1" {
				hasWidth = true
			}
		}

		return hasHeight && hasWidth
	}

	return false
}

var requiredAttributes = map[string][]string{
	"a":      {"href"},
	"iframe": {"src"},
	"img":    {"src"},
	"source": {"src", "srcset"},
}

func hasRequiredAttributes(tagName string, attributes []string) bool {
	if attrs, ok := requiredAttributes[tagName]; ok {
		for _, attribute := range attributes {
			for _, attr := range attrs {
				if attr == attribute {
					return true
				}
			}
		}

		return false
	}

	return true
}

// See https://www.iana.org/assignments/uri-schemes/uri-schemes.xhtml
func hasValidURIScheme(src string) bool {
	whitelist := []string{
		"apt:",
		"bitcoin:",
		"callto:",
		"dav:",
		"davs:",
		"ed2k://",
		"facetime://",
		"feed:",
		"ftp://",
		"geo:",
		"gopher://",
		"git://",
		"http://",
		"https://",
		"irc://",
		"irc6://",
		"ircs://",
		"itms://",
		"itms-apps://",
		"magnet:",
		"mailto:",
		"news:",
		"nntp:",
		"rtmp://",
		"sip:",
		"sips:",
		"skype:",
		"spotify:",
		"ssh://",
		"sftp://",
		"steam://",
		"svn://",
		"svn+ssh://",
		"tel:",
		"webcal://",
		"xmpp:",
	}

	for _, prefix := range whitelist {
		if strings.HasPrefix(src, prefix) {
			return true
		}
	}

	return false
}

func isBlockedResource(src string) bool {
	blacklist := []string{
		"feedsportal.com",
		"api.flattr.com",
		"stats.wordpress.com",
		"plus.google.com/share",
		"twitter.com/share",
		"feeds.feedburner.com",
	}

	for _, element := range blacklist {
		if strings.Contains(src, element) {
			return true
		}
	}

	return false
}

func isValidIframeSource(baseURL, src string) bool {
	whitelist := []string{
		"//www.youtube.com",
		"http://www.youtube.com",
		"https://www.youtube.com",
		"https://www.youtube-nocookie.com",
		"http://player.vimeo.com",
		"https://player.vimeo.com",
		"http://www.dailymotion.com",
		"https://www.dailymotion.com",
		"http://vk.com",
		"https://vk.com",
		"http://soundcloud.com",
		"https://soundcloud.com",
		"http://w.soundcloud.com",
		"https://w.soundcloud.com",
		"http://bandcamp.com",
		"https://bandcamp.com",
		"https://cdn.embedly.com",
		"https://player.bilibili.com",
	}

	// allow iframe from same origin
	if url.Domain(baseURL) == url.Domain(src) {
		return true
	}

	// allow iframe from custom invidious instance
	if config.Opts != nil && config.Opts.InvidiousInstance() == url.Domain(src) {
		return true
	}

	for _, prefix := range whitelist {
		if strings.HasPrefix(src, prefix) {
			return true
		}
	}

	return false
}

var tagAllowList = map[string][]string{
	"img":        {"alt", "title", "src", "srcset", "sizes", "width", "height"},
	"picture":    {},
	"audio":      {"src"},
	"video":      {"poster", "height", "width", "src"},
	"source":     {"src", "type", "srcset", "sizes", "media"},
	"dt":         {"id"},
	"dd":         {"id"},
	"dl":         {"id"},
	"table":      {},
	"caption":    {},
	"thead":      {},
	"tfooter":    {},
	"tr":         {},
	"td":         {"rowspan", "colspan"},
	"th":         {"rowspan", "colspan"},
	"h1":         {"id"},
	"h2":         {"id"},
	"h3":         {"id"},
	"h4":         {"id"},
	"h5":         {"id"},
	"h6":         {"id"},
	"strong":     {},
	"em":         {},
	"code":       {},
	"pre":        {},
	"blockquote": {},
	"q":          {"cite"},
	"p":          {},
	"ul":         {"id"},
	"li":         {"id"},
	"ol":         {"id"},
	"br":         {},
	"del":        {},
	"a":          {"href", "title", "id"},
	"figure":     {},
	"figcaption": {},
	"cite":       {},
	"time":       {"datetime"},
	"abbr":       {"title"},
	"acronym":    {"title"},
	"wbr":        {},
	"dfn":        {},
	"sub":        {},
	"sup":        {},
	"var":        {},
	"samp":       {},
	"s":          {},
	"ins":        {},
	"kbd":        {},
	"rp":         {},
	"rt":         {},
	"rtc":        {},
	"ruby":       {},
	"iframe":     {"width", "height", "frameborder", "src", "allowfullscreen"},
}

func inList(needle string, haystack []string) bool {
	for _, element := range haystack {
		if element == needle {
			return true
		}
	}

	return false
}

func rewriteIframeURL(link string) string {
	matches := youtubeEmbedRegex.FindStringSubmatch(link)
	if len(matches) == 2 {
		return `https://www.youtube-nocookie.com/embed/` + matches[1]
	}

	return link
}

func isBlockedTag(tagName string) bool {
	blacklist := []string{
		"noscript",
		"script",
		"style",
	}

	for _, element := range blacklist {
		if element == tagName {
			return true
		}
	}

	return false
}

func sanitizeSrcsetAttr(baseURL, value string) string {
	imageCandidates := ParseSrcSetAttribute(value)

	for _, imageCandidate := range imageCandidates {
		absoluteURL, err := url.AbsoluteURL(baseURL, imageCandidate.ImageURL)
		if err == nil {
			imageCandidate.ImageURL = absoluteURL
		}
	}

	return imageCandidates.String()
}

func isValidDataAttribute(value string) bool {
	var dataAttributeAllowList = []string{
		"data:image/avif",
		"data:image/apng",
		"data:image/png",
		"data:image/svg",
		"data:image/svg+xml",
		"data:image/jpg",
		"data:image/jpeg",
		"data:image/gif",
		"data:image/webp",
	}

	for _, prefix := range dataAttributeAllowList {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func isAnchor(tagName string, attribute html.Attribute) bool {
	return tagName == "a" && attribute.Key == "href" && strings.HasPrefix(attribute.Val, "#")
}

func isPositiveInteger(value string) bool {
	if number, err := strconv.Atoi(value); err == nil {
		return number > 0
	}
	return false
}

func getAttributeValue(name string, attributes []html.Attribute) string {
	for _, attribute := range attributes {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func getIntegerAttributeValue(name string, attributes []html.Attribute) int {
	number, _ := strconv.Atoi(getAttributeValue(name, attributes))
	return number
}
