package webmail

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/mjl-/bstore"
)

func documentPreview(ctx context.Context, id int64, path []int) map[string]any {
	ri := ctx.Value(requestInfoCtxKey).(requestInfo)
	var data []byte
	var mimeType string
	xdbread(ctx, ri.Account, func(tx *bstore.Tx) {
		m := xmessageID(ctx, tx, id)
		s := msgState{acc: ri.Account, log: ri.Log}
		defer s.clear()
		organizerCheck(ctx, !s.ensurePart(m, true), "cannot read attachment")
		p := s.part
		for _, i := range path {
			organizerCheck(ctx, i < 0 || i >= len(p.Parts), "invalid attachment path")
			p = &p.Parts[i]
		}
		mimeType = strings.ToLower(p.MediaType + "/" + p.MediaSubType)
		var err error
		data, err = io.ReadAll(io.LimitReader(p.ReaderUTF8OrBinary(), 10*1024*1024+1))
		xcheckf(ctx, err, "read document")
		organizerCheck(ctx, len(data) > 10*1024*1024, "preview limit is 10 MB")
	})
	if strings.HasPrefix(mimeType, "text/") || mimeType == "application/json" || mimeType == "application/xml" {
		return map[string]any{"Text": string(data), "MimeType": mimeType}
	}
	text, err := officeText(data)
	xcheckuserf(ctx, err, "document preview unavailable; download to open")
	return map[string]any{"Text": text, "MimeType": mimeType}
}

// Office previews extract text locally. No document is uploaded to a preview
// service. ZIP and XML reads have independent caps against decompression bombs.
func officeText(data []byte) (string, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var out strings.Builder
	var shared []string
	remaining := int64(4 * 1024 * 1024)
	readXML := func(file *zip.File) ([]byte, error) {
		if file.UncompressedSize64 > uint64(remaining) {
			return nil, fmt.Errorf("document too large")
		}
		r, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		buf, err := io.ReadAll(io.LimitReader(r, remaining+1))
		remaining -= int64(len(buf))
		if remaining < 0 {
			return nil, fmt.Errorf("document too large")
		}
		return buf, err
	}
	for _, file := range z.File {
		if file.Name == "xl/sharedStrings.xml" {
			buf, err := readXML(file)
			if err != nil {
				return "", err
			}
			var values struct {
				Items []struct {
					Text string `xml:"t"`
					Runs []struct {
						Text string `xml:"t"`
					} `xml:"r"`
				} `xml:"si"`
			}
			if err := xml.Unmarshal(buf, &values); err != nil {
				return "", err
			}
			for _, item := range values.Items {
				text := item.Text
				for _, run := range item.Runs {
					text += run.Text
				}
				shared = append(shared, text)
			}
		}
	}
	for _, file := range z.File {
		name := file.Name
		if name != "word/document.xml" && name != "content.xml" && !(strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml")) && !(strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml")) {
			continue
		}
		buf, err := readXML(file)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(name, "xl/") {
			var sheet struct {
				Rows []struct {
					Cells []struct {
						Type   string `xml:"t,attr"`
						Value  string `xml:"v"`
						Inline struct {
							Text string `xml:"t"`
						} `xml:"is"`
					} `xml:"c"`
				} `xml:"sheetData>row"`
			}
			if err := xml.Unmarshal(buf, &sheet); err != nil {
				return "", err
			}
			out.WriteString(name + "\n")
			for _, row := range sheet.Rows {
				for i, c := range row.Cells {
					if i > 0 {
						out.WriteString("\t")
					}
					value := c.Value
					if c.Type == "s" {
						var n int
						if _, err := fmt.Sscanf(c.Value, "%d", &n); err == nil && n >= 0 && n < len(shared) {
							value = shared[n]
						}
					} else if c.Type == "inlineStr" {
						value = c.Inline.Text
					}
					out.WriteString(value)
				}
				out.WriteByte('\n')
			}
			continue
		}
		d := xml.NewDecoder(bytes.NewReader(buf))
		inside := false
		odfDepth := 0
		for {
			token, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			switch t := token.(type) {
			case xml.StartElement:
				if t.Name.Local == "t" {
					inside = true
				}
				if name == "content.xml" && (t.Name.Local == "p" || t.Name.Local == "h") {
					odfDepth++
				}
				if t.Name.Local == "br" || t.Name.Local == "tab" {
					out.WriteByte('\n')
				}
			case xml.EndElement:
				if t.Name.Local == "t" {
					inside = false
				}
				if t.Name.Local == "p" || t.Name.Local == "h" || t.Name.Local == "tr" {
					out.WriteByte('\n')
				}
				if name == "content.xml" && (t.Name.Local == "p" || t.Name.Local == "h") {
					odfDepth--
				}
			case xml.CharData:
				if inside || odfDepth > 0 {
					out.Write(t)
				}
			}
		}
		out.WriteByte('\n')
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", fmt.Errorf("unsupported document format")
	}
	return strings.TrimSpace(out.String()), nil
}
