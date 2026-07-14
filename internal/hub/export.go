package hub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxExportBytes int64 = 8 << 20

// handleDownload serves the original blob or a knowledge-worker-friendly
// export. Generated formats are deliberately limited to text-like files;
// binary previews keep their original download only.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, user, repo, filePath string) {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "original"
	}
	if format == "original" {
		setAttachmentDisposition(w, pathBase(filePath))
		s.handleRaw(w, user, repo, filePath)
		return
	}

	content, ok := s.exportText(user, repo, filePath)
	if !ok {
		http.Error(w, "this file cannot be exported as text", http.StatusUnsupportedMediaType)
		return
	}

	var body []byte
	var contentType, filename string
	switch format {
	case "md", "markdown":
		if bundle, assets, err := s.markdownBundle(user, repo, filePath, content); err != nil {
			s.Log.Printf("markdown bundle %s/%s/%s: %v", user, repo, filePath, err)
			http.Error(w, "could not package the Markdown document", http.StatusInternalServerError)
			return
		} else if assets > 0 {
			body = bundle
			contentType = "application/zip"
			filename = exportFilename(filePath, ".zip")
		} else {
			body = []byte(content)
			contentType = "text/markdown; charset=utf-8"
			filename = exportFilename(filePath, ".md")
		}
	case "pdf":
		body = markdownPDF(content, s.exportImageResolver(user, repo, filePath))
		contentType = "application/pdf"
		filename = exportFilename(filePath, ".pdf")
	case "doc", "docx", "word":
		var err error
		body, err = markdownDocx(content, s.exportImageResolver(user, repo, filePath))
		if err != nil {
			s.Log.Printf("docx export %s/%s/%s: %v", user, repo, filePath, err)
			http.Error(w, "could not create the Word document", http.StatusInternalServerError)
			return
		}
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		filename = exportFilename(filePath, ".docx")
	default:
		http.Error(w, "unsupported download format", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	setAttachmentDisposition(w, filename)
	_, _ = w.Write(body)
}

func (s *Server) exportText(user, repo, filePath string) (string, bool) {
	bare := s.Storage.RepoDir(user, repo)
	size, ok := BlobSize("git", bare, defaultRef, filePath)
	if !ok || size > maxExportBytes {
		return "", false
	}
	content, ok := BlobContent("git", bare, defaultRef, filePath)
	if !ok {
		return "", false
	}
	if ptr, isPtr := ParseLFSPointer(content); isPtr {
		if s.LFS == nil {
			return "", false
		}
		rc, objectSize, err := s.LFS.Open(user, repo, ptr.OID, ptr.Size)
		if err != nil || objectSize > maxExportBytes {
			return "", false
		}
		defer rc.Close()
		body, err := io.ReadAll(io.LimitReader(rc, maxExportBytes+1))
		if err != nil || int64(len(body)) > maxExportBytes {
			return "", false
		}
		content = string(body)
	}
	if !exportableText(filePath, content) {
		return "", false
	}
	return content, true
}

func (s *Server) exportImageResolver(user, repo, markdownPath string) exportImageResolver {
	bare := s.Storage.RepoDir(user, repo)
	return func(target string) (exportImage, bool) {
		rel, ok := repositoryAssetPath(markdownPath, target)
		if !ok {
			return exportImage{}, false
		}
		size, ok := BlobSize("git", bare, defaultRef, rel)
		if !ok || size > maxExportBytes {
			return exportImage{}, false
		}
		content, ok := BlobContent("git", bare, defaultRef, rel)
		if !ok {
			return exportImage{}, false
		}
		body := []byte(content)
		if ptr, isPtr := ParseLFSPointer(content); isPtr {
			if s.LFS == nil || ptr.Size > maxExportBytes {
				return exportImage{}, false
			}
			rc, objectSize, err := s.LFS.Open(user, repo, ptr.OID, ptr.Size)
			if err != nil || objectSize > maxExportBytes {
				return exportImage{}, false
			}
			defer rc.Close()
			body, err = io.ReadAll(io.LimitReader(rc, maxExportBytes+1))
			if err != nil || int64(len(body)) > maxExportBytes {
				return exportImage{}, false
			}
		}
		contentType := fileContentType(rel)
		if !strings.HasPrefix(contentType, "image/") {
			return exportImage{}, false
		}
		return exportImage{Data: body, ContentType: contentType}, true
	}
}

func repositoryAssetPath(markdownPath, target string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "#") {
		return "", false
	}
	rel := path.Clean(path.Join(path.Dir(markdownPath), u.Path))
	return rel, validRepoPath(rel)
}

func (s *Server) markdownBundle(user, repo, filePath, content string) ([]byte, int, error) {
	resolver := s.exportImageResolver(user, repo, filePath)
	targets := markdownImageTargets(content)
	assets := make(map[string]exportImage)
	for _, target := range targets {
		rel, ok := repositoryAssetPath(filePath, target)
		if !ok {
			continue
		}
		asset, ok := resolver(target)
		if ok {
			assets[rel] = asset
		}
	}
	if len(assets) == 0 {
		return nil, 0, nil
	}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	noteName := path.Clean(filePath)
	w, err := zw.Create(noteName)
	if err != nil {
		return nil, 0, err
	}
	if _, err := io.WriteString(w, content); err != nil {
		return nil, 0, err
	}
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			return nil, 0, err
		}
		if _, err := w.Write(assets[name].Data); err != nil {
			return nil, 0, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, 0, err
	}
	return out.Bytes(), len(assets), nil
}

func markdownImageTargets(content string) []string {
	content = stripFrontmatter(strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n"))
	targets := make([]string, 0)
	for len(content) > 0 {
		start := strings.Index(content, "![")
		if start < 0 {
			break
		}
		content = content[start:]
		closeLabel := strings.Index(content, "](")
		if closeLabel < 2 {
			content = content[2:]
			continue
		}
		closeDestination := strings.IndexByte(content[closeLabel+2:], ')')
		if closeDestination < 0 {
			break
		}
		closeDestination += closeLabel + 2
		if _, target, ok := markdownStandaloneImage(content[:closeDestination+1]); ok {
			targets = append(targets, target)
		}
		content = content[closeDestination+1:]
	}
	return targets
}

func exportableText(filePath, content string) bool {
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return false
	}
	ext := strings.ToLower(path.Ext(filePath))
	if ext == ".md" || ext == ".markdown" || ext == ".mdown" {
		return true
	}
	return shouldServeRawAsText(filePath, content)
}

func exportFilename(filePath, ext string) string {
	base := pathBase(filePath)
	if old := path.Ext(base); old != "" {
		base = strings.TrimSuffix(base, old)
	}
	return base + ext
}

func setAttachmentDisposition(w http.ResponseWriter, filename string) {
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if disposition == "" {
		disposition = "attachment; filename=\"" + dispositionName(filename) + "\""
	}
	w.Header().Set("Content-Disposition", disposition)
}

type exportLine struct {
	Text, Kind, Marker, ImageTarget string
	Cells                           []string
}

type exportImage struct {
	Data        []byte
	ContentType string
}

type exportImageResolver func(target string) (exportImage, bool)

func markdownExportLines(content string) []exportLine {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = stripFrontmatter(content)
	rawLines := strings.Split(content, "\n")
	lines := make([]exportLine, 0, len(rawLines))
	inFence := false
	for i, raw := range rawLines {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if trimmed == "" {
			lines = append(lines, exportLine{})
			continue
		}
		if !inFence {
			if alt, target, ok := markdownStandaloneImage(trimmed); ok {
				lines = append(lines, exportLine{Kind: "image", Text: alt, ImageTarget: target})
				continue
			}
		}
		if cells, ok := markdownTableRow(trimmed); !inFence && ok {
			if markdownTableDelimiter(cells) {
				continue
			}
			kind := "tableRow"
			if i+1 < len(rawLines) {
				if next, ok := markdownTableRow(strings.TrimSpace(rawLines[i+1])); ok && markdownTableDelimiter(next) {
					kind = "tableHeader"
				}
			}
			for j := range cells {
				cells[j] = stripInlineMarkdown(cells[j])
			}
			lines = append(lines, exportLine{Kind: kind, Cells: cells})
			continue
		}
		kind := "body"
		text := trimmed
		marker := ""
		if inFence {
			kind = "code"
			text = strings.TrimRight(raw, " \t")
		} else if strings.HasPrefix(text, "#") {
			level := 0
			for level < len(text) && text[level] == '#' {
				level++
			}
			if level > 0 && level < len(text) && text[level] == ' ' {
				if level > 3 {
					level = 3
				}
				kind = fmt.Sprintf("heading%d", level)
				text = strings.TrimSpace(text[level:])
			}
		}
		if !inFence && (strings.HasPrefix(text, "- ") || strings.HasPrefix(text, "* ") || strings.HasPrefix(text, "+ ")) {
			kind, marker, text = "bullet", "*", strings.TrimSpace(text[2:])
		} else if !inFence {
			if number, rest, ok := orderedListItem(text); ok {
				kind, marker, text = "number", number+".", rest
			}
		}
		if !inFence {
			text = stripInlineMarkdown(text)
		}
		lines = append(lines, exportLine{Text: text, Kind: kind, Marker: marker})
	}
	for len(lines) > 0 && lines[len(lines)-1].Text == "" && len(lines[len(lines)-1].Cells) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []exportLine{{}}
	}
	return lines
}

func markdownStandaloneImage(text string) (alt, target string, ok bool) {
	if !strings.HasPrefix(text, "![") || !strings.HasSuffix(text, ")") {
		return "", "", false
	}
	closeLabel := strings.Index(text, "](")
	if closeLabel < 2 {
		return "", "", false
	}
	alt = strings.TrimSpace(text[2:closeLabel])
	destination := strings.TrimSpace(text[closeLabel+2 : len(text)-1])
	if destination == "" {
		return "", "", false
	}
	// A trailing quoted title is metadata, not part of the repository path.
	if quote := strings.Index(destination, ` "`); quote >= 0 {
		destination = strings.TrimSpace(destination[:quote])
	} else if quote := strings.Index(destination, " '"); quote >= 0 {
		destination = strings.TrimSpace(destination[:quote])
	}
	target = strings.Trim(destination, "<>")
	if target == "" {
		return "", "", false
	}
	if alt == "" {
		alt = "Repository image"
	}
	return alt, target, true
}

func markdownTableRow(text string) ([]string, bool) {
	if !strings.HasPrefix(text, "|") || !strings.HasSuffix(text, "|") {
		return nil, false
	}
	parts := strings.Split(strings.Trim(text, "|"), "|")
	if len(parts) < 2 {
		return nil, false
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts, true
}

func markdownTableDelimiter(cells []string) bool {
	for _, cell := range cells {
		cell = strings.Trim(cell, ":")
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return len(cells) > 0
}

func orderedListItem(text string) (string, string, bool) {
	i := 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(text) || (text[i] != '.' && text[i] != ')') || text[i+1] != ' ' {
		return "", "", false
	}
	return text[:i], strings.TrimSpace(text[i+2:]), true
}

func stripInlineMarkdown(text string) string {
	// Keep link and image labels while dropping their destinations. This is a
	// deliberately small inline pass, but it handles the common Markdown form
	// without leaking URLs into generated prose.
	for {
		closeLabel := strings.Index(text, "](")
		if closeLabel < 0 {
			break
		}
		openLabel := strings.LastIndex(text[:closeLabel], "[")
		closeURL := strings.IndexByte(text[closeLabel+2:], ')')
		if openLabel < 0 || closeURL < 0 {
			break
		}
		closeURL += closeLabel + 2
		prefixEnd := openLabel
		if openLabel > 0 && text[openLabel-1] == '!' {
			prefixEnd--
		}
		label := text[openLabel+1 : closeLabel]
		text = text[:prefixEnd] + label + text[closeURL+1:]
	}
	for {
		start := strings.Index(text, "[[")
		if start < 0 {
			break
		}
		end := strings.Index(text[start+2:], "]]")
		if end < 0 {
			break
		}
		end += start + 2
		inner := text[start+2 : end]
		label := inner
		if pipe := strings.IndexByte(inner, '|'); pipe >= 0 {
			label = inner[pipe+1:]
		}
		text = text[:start] + strings.TrimSpace(label) + text[end+2:]
	}
	for _, marker := range []string{"**", "__", "~~", "`"} {
		text = strings.ReplaceAll(text, marker, "")
	}
	for _, marker := range []string{"*", "_"} {
		if len(text) >= 2 && strings.HasPrefix(text, marker) && strings.HasSuffix(text, marker) {
			text = strings.TrimSpace(text[1 : len(text)-1])
		}
	}
	return strings.TrimSpace(text)
}

type pdfRaster struct {
	Name          string
	Data          []byte
	Width, Height int
}

type pdfLayout struct {
	pages    []*strings.Builder
	y        int
	images   []pdfRaster
	resolver exportImageResolver
}

func markdownPDF(content string, imageResolvers ...exportImageResolver) []byte {
	var resolver exportImageResolver
	if len(imageResolvers) > 0 {
		resolver = imageResolvers[0]
	}
	layout := &pdfLayout{resolver: resolver}
	layout.addPage()
	lines := markdownExportLines(content)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch line.Kind {
		case "tableHeader", "tableRow":
			j := i + 1
			for j < len(lines) && (lines[j].Kind == "tableHeader" || lines[j].Kind == "tableRow") {
				j++
			}
			layout.addTable(lines[i:j])
			i = j - 1
		case "image":
			if !layout.addImage(line) {
				layout.addText(exportLine{Kind: "body", Text: "Image unavailable: " + line.Text})
			}
		default:
			layout.addText(line)
		}
	}
	return layout.bytes()
}

func (p *pdfLayout) addPage() {
	p.pages = append(p.pages, &strings.Builder{})
	p.y = 744
}

func (p *pdfLayout) page() *strings.Builder { return p.pages[len(p.pages)-1] }

func (p *pdfLayout) ensureHeight(height int) bool {
	if p.y-height >= 54 {
		return false
	}
	p.addPage()
	return true
}

func (p *pdfLayout) addText(line exportLine) {
	font, size, step := pdfLineMetrics(line.Kind)
	text := pdfSafeText(line.Text)
	width := pdfWrapWidth(line.Kind)
	isListItem := line.Kind == "bullet" || line.Kind == "number"
	if isListItem {
		width -= 3
	}
	wrapped := wrapExportText(text, width)
	if len(wrapped) == 0 {
		p.ensureHeight(step)
		p.y -= step
		return
	}
	for index, part := range wrapped {
		p.ensureHeight(step)
		x := 54
		if isListItem {
			x = 70
			if index == 0 {
				p.addListMarker(line, font, size)
			}
		} else if index == 0 && line.Marker != "" {
			part = line.Marker + " " + part
		}
		fmt.Fprintf(p.page(), "BT /%s %d Tf %d %d Td (%s) Tj ET\n", font, size, x, p.y, pdfEscape(part))
		p.y -= step
	}
}

func (p *pdfLayout) addListMarker(line exportLine, font string, size int) {
	const markerRight = 65.0
	marker, markerX := line.Marker, markerRight-pdfHelveticaMarkerWidth(line.Marker, size)
	if line.Kind == "bullet" {
		// WinAnsi byte 0x95 is a proper bullet in Helvetica. Keep the
		// octal escape outside pdfEscape so the PDF parser sees it.
		marker, markerX = `\225`, markerRight-3.5
	}
	fmt.Fprintf(p.page(), "BT /%s %d Tf %.1f %d Td (%s) Tj ET\n", font, size, markerX, p.y, marker)
}

func pdfHelveticaMarkerWidth(marker string, size int) float64 {
	units := 0
	for _, r := range marker {
		switch {
		case r >= '0' && r <= '9':
			units += 556
		case r == '.':
			units += 278
		default:
			units += 556
		}
	}
	return float64(units*size) / 1000
}

func (p *pdfLayout) addTable(rows []exportLine) {
	columns := 0
	for _, row := range rows {
		if len(row.Cells) > columns {
			columns = len(row.Cells)
		}
	}
	if columns == 0 {
		return
	}
	dxaWidths := docxTableWidths(rows, columns, 9240)
	widths := make([]int, columns)
	allocated := 0
	for i, width := range dxaWidths {
		if i == columns-1 {
			widths[i] = 504 - allocated
		} else {
			widths[i] = width * 504 / 9240
			allocated += widths[i]
		}
	}
	header := exportLine{}
	if len(rows) > 0 && rows[0].Kind == "tableHeader" {
		header = rows[0]
	}
	for rowIndex, row := range rows {
		cellLines, rowHeight := pdfTableCellLines(row, widths)
		newPage := p.ensureHeight(rowHeight + 8)
		if newPage && rowIndex > 0 && header.Kind != "" {
			headerLines, headerHeight := pdfTableCellLines(header, widths)
			p.drawTableRow(header, headerLines, widths, headerHeight)
		}
		p.drawTableRow(row, cellLines, widths, rowHeight)
	}
	p.y -= 10
}

func pdfTableCellLines(row exportLine, widths []int) ([][]string, int) {
	cellLines := make([][]string, len(widths))
	maxLines := 1
	for column, width := range widths {
		text := ""
		if column < len(row.Cells) {
			text = pdfSafeText(row.Cells[column])
		}
		chars := (width - 12) / 5
		if chars < 8 {
			chars = 8
		}
		cellLines[column] = wrapExportText(text, chars)
		if len(cellLines[column]) == 0 {
			cellLines[column] = []string{""}
		}
		if len(cellLines[column]) > maxLines {
			maxLines = len(cellLines[column])
		}
	}
	return cellLines, maxLines*11 + 10
}

func (p *pdfLayout) drawTableRow(row exportLine, cellLines [][]string, widths []int, height int) {
	x := 54
	header := row.Kind == "tableHeader"
	for column, width := range widths {
		if header {
			fmt.Fprintf(p.page(), "q 0.91 0.94 0.92 rg %d %d %d %d re f Q\n", x, p.y-height, width, height)
		}
		fmt.Fprintf(p.page(), "q 0.72 0.76 0.73 RG 0.5 w %d %d %d %d re S Q\n", x, p.y-height, width, height)
		font := "F1"
		if header {
			font = "F2"
		}
		for lineIndex, text := range cellLines[column] {
			if text == "" {
				continue
			}
			fmt.Fprintf(p.page(), "BT /%s 8 Tf %d %d Td (%s) Tj ET\n", font, x+6, p.y-13-lineIndex*11, pdfEscape(text))
		}
		x += width
	}
	p.y -= height
}

func (p *pdfLayout) addImage(line exportLine) bool {
	if p.resolver == nil {
		return false
	}
	asset, ok := p.resolver(line.ImageTarget)
	if !ok {
		return false
	}
	raster, ok := rasterForPDF(asset.Data)
	if !ok {
		return false
	}
	displayWidth := raster.Width * 72 / 96
	displayHeight := raster.Height * 72 / 96
	if displayWidth > 504 {
		displayHeight = displayHeight * 504 / displayWidth
		displayWidth = 504
	}
	if displayHeight > 300 {
		displayWidth = displayWidth * 300 / displayHeight
		displayHeight = 300
	}
	if displayWidth < 1 || displayHeight < 1 {
		return false
	}
	p.ensureHeight(displayHeight + 16)
	raster.Name = fmt.Sprintf("Im%d", len(p.images)+1)
	p.images = append(p.images, raster)
	x := 54 + (504-displayWidth)/2
	fmt.Fprintf(p.page(), "q %d 0 0 %d %d %d cm /%s Do Q\n", displayWidth, displayHeight, x, p.y-displayHeight, raster.Name)
	p.y -= displayHeight + 16
	return true
}

func rasterForPDF(data []byte) (pdfRaster, bool) {
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return pdfRaster{}, false
	}
	bounds := decoded.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 {
		return pdfRaster{}, false
	}
	canvas := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(canvas, canvas.Bounds(), decoded, bounds.Min, draw.Over)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, canvas, &jpeg.Options{Quality: 86}); err != nil {
		return pdfRaster{}, false
	}
	return pdfRaster{Data: encoded.Bytes(), Width: bounds.Dx(), Height: bounds.Dy()}, true
}

func (p *pdfLayout) bytes() []byte {
	objects := []string{"", "<< /Type /Catalog /Pages 2 0 R >>", "", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>"}
	imageObjects := make(map[string]int, len(p.images))
	for _, raster := range p.images {
		objectNumber := len(objects)
		imageObjects[raster.Name] = objectNumber
		objects = append(objects, fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", raster.Width, raster.Height, len(raster.Data))+string(raster.Data)+"\nendstream")
	}
	var xObjects strings.Builder
	if len(imageObjects) > 0 {
		xObjects.WriteString(" /XObject <<")
		for _, raster := range p.images {
			fmt.Fprintf(&xObjects, " /%s %d 0 R", raster.Name, imageObjects[raster.Name])
		}
		xObjects.WriteString(" >>")
	}
	kids := make([]string, 0, len(p.pages))
	for pageIndex, commands := range p.pages {
		stream := commands.String() + fmt.Sprintf("BT /F1 8 Tf 54 30 Td (Page %d of %d) Tj ET\n", pageIndex+1, len(p.pages))
		pageObject := len(objects)
		objects = append(objects, "")
		contentObject := len(objects)
		objects = append(objects, "")
		objects[pageObject] = fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R /F2 4 0 R >>%s >> /Contents %d 0 R >>", xObjects.String(), contentObject)
		objects[contentObject] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObject))
	}
	objects[2] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objects))
	for i := 1; i < len(objects); i++ {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i, objects[i])
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects))
	for i := 1; i < len(objects); i++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects), xref)
	return out.Bytes()
}

func pdfWrapWidth(kind string) int {
	switch kind {
	case "heading1":
		return 48
	case "heading2":
		return 62
	case "heading3":
		return 76
	case "code":
		return 96
	default:
		return 88
	}
}

func pdfLineMetrics(kind string) (font string, size, step int) {
	font, size, step = "F1", 10, 15
	switch kind {
	case "heading1":
		font, size, step = "F2", 18, 25
	case "heading2":
		font, size, step = "F2", 14, 21
	case "heading3":
		font, size, step = "F2", 11, 17
	case "code":
		size, step = 9, 13
	case "tableHeader":
		font, size, step = "F2", 10, 15
	}
	return
}

func paginatePDFLines(lines []exportLine) [][]exportLine {
	const usableHeight = 680
	pages := make([][]exportLine, 0, 1)
	page := make([]exportLine, 0, 45)
	used := 0
	for _, line := range lines {
		_, _, step := pdfLineMetrics(line.Kind)
		if len(page) > 0 && used+step > usableHeight {
			pages = append(pages, page)
			page = make([]exportLine, 0, 45)
			used = 0
		}
		page = append(page, line)
		used += step
	}
	if len(page) > 0 {
		pages = append(pages, page)
	}
	return pages
}

func pdfPageStream(lines []exportLine, pageNumber, pageCount int) string {
	var out strings.Builder
	y := 744
	for _, line := range lines {
		font, size, step := pdfLineMetrics(line.Kind)
		if line.Text != "" {
			fmt.Fprintf(&out, "BT /%s %d Tf 54 %d Td (%s) Tj ET\n", font, size, y, pdfEscape(line.Text))
		}
		y -= step
	}
	fmt.Fprintf(&out, "BT /F1 8 Tf 54 30 Td (Page %d of %d) Tj ET\n", pageNumber, pageCount)
	return out.String()
}

func pdfSafeText(text string) string {
	var out strings.Builder
	for _, r := range text {
		switch r {
		case '\t':
			out.WriteString("    ")
		case '—', '–', '‑':
			out.WriteByte('-')
		case '’', '‘':
			out.WriteByte('\'')
		case '“', '”':
			out.WriteByte('"')
		case '…':
			out.WriteString("...")
		case '•':
			out.WriteByte('*')
		default:
			if r >= 0x20 && r <= 0x7e {
				out.WriteRune(r)
			} else if r >= 0xa0 && r <= 0xff {
				// WinAnsiEncoding preserves common accented Latin text.
				out.WriteByte(byte(r))
			} else {
				out.WriteByte('?')
			}
		}
	}
	return out.String()
}

func pdfEscape(text string) string {
	text = strings.ReplaceAll(text, "\\", "\\\\")
	text = strings.ReplaceAll(text, "(", "\\(")
	return strings.ReplaceAll(text, ")", "\\)")
}

func wrapExportText(text string, width int) []string {
	if text == "" {
		return nil
	}
	if len(text) <= width {
		return []string{text}
	}
	var out []string
	for len(text) > width {
		cut := strings.LastIndexByte(text[:width+1], ' ')
		if cut < 1 {
			cut = width
		}
		out = append(out, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}

type docxMedia struct {
	Name, RelID, Alt    string
	Data                []byte
	WidthEMU, HeightEMU int64
}

func markdownDocx(content string, imageResolvers ...exportImageResolver) ([]byte, error) {
	var resolver exportImageResolver
	if len(imageResolvers) > 0 {
		resolver = imageResolvers[0]
	}
	var document strings.Builder
	document.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><w:body>`)
	media := make([]docxMedia, 0)
	lines := markdownExportLines(content)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line.Kind == "tableHeader" || line.Kind == "tableRow" {
			j := i + 1
			for j < len(lines) && (lines[j].Kind == "tableHeader" || lines[j].Kind == "tableRow") {
				j++
			}
			document.WriteString(docxTable(lines[i:j]))
			i = j - 1
			continue
		}
		if line.Kind == "image" {
			if resolver != nil {
				if asset, ok := resolver(line.ImageTarget); ok {
					if imagePart, ok := rasterForDocx(asset.Data, len(media)+1, line.Text); ok {
						media = append(media, imagePart)
						document.WriteString(docxImageParagraph(imagePart, len(media)))
						continue
					}
				}
			}
			fmt.Fprintf(&document, `<w:p><w:pPr><w:pStyle w:val="Normal"/><w:shd w:val="clear" w:color="auto" w:fill="F3F4F2"/><w:spacing w:before="80" w:after="120"/></w:pPr><w:r><w:rPr><w:i/><w:color w:val="5F6962"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, xmlText("Image unavailable: "+line.Text))
			continue
		}
		if line.Text == "" {
			document.WriteString("<w:p/>")
			continue
		}
		style := "Normal"
		switch line.Kind {
		case "heading1":
			style = "Heading1"
		case "heading2":
			style = "Heading2"
		case "heading3":
			style = "Heading3"
		case "code":
			style = "Code"
		case "bullet":
			fmt.Fprintf(&document, `<w:p><w:pPr><w:pStyle w:val="Normal"/><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, xmlText(line.Text))
			continue
		case "number":
			fmt.Fprintf(&document, `<w:p><w:pPr><w:pStyle w:val="Normal"/><w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr></w:pPr><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, xmlText(line.Text))
			continue
		}
		fmt.Fprintf(&document, `<w:p><w:pPr><w:pStyle w:val="%s"/></w:pPr><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, style, xmlText(line.Text))
	}
	document.WriteString(`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr></w:body></w:document>`)

	styles := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:eastAsia="Arial" w:cs="Arial"/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial"/><w:sz w:val="22"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="280" w:after="120"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial"/><w:b/><w:sz w:val="34"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="220" w:after="100"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial"/><w:b/><w:sz w:val="28"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="180" w:after="80"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial"/><w:b/><w:sz w:val="24"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Code"><w:name w:val="Code"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="60" w:after="60" w:line="240" w:lineRule="auto"/><w:ind w:left="360"/></w:pPr><w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New"/><w:sz w:val="19"/></w:rPr></w:style></w:styles>`
	numbering := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="singleLevel"/><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="•"/><w:lvlJc w:val="left"/><w:pPr><w:tabs><w:tab w:val="num" w:pos="720"/></w:tabs><w:ind w:left="720" w:hanging="360"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial"/></w:rPr></w:lvl></w:abstractNum><w:abstractNum w:abstractNumId="1"><w:multiLevelType w:val="singleLevel"/><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/><w:lvlJc w:val="left"/><w:pPr><w:tabs><w:tab w:val="num" w:pos="720"/></w:tabs><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num><w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num></w:numbering>`
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/></Types>`
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`
	var documentRels strings.Builder
	documentRels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>`)
	for _, imagePart := range media {
		fmt.Fprintf(&documentRels, `<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/%s"/>`, imagePart.RelID, imagePart.Name)
	}
	documentRels.WriteString(`</Relationships>`)

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	entries := []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rels},
		{"word/document.xml", document.String()},
		{"word/styles.xml", styles},
		{"word/numbering.xml", numbering},
		{"word/_rels/document.xml.rels", documentRels.String()},
	}
	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(w, entry.body); err != nil {
			return nil, err
		}
	}
	for _, imagePart := range media {
		w, err := zw.Create("word/media/" + imagePart.Name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(imagePart.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func rasterForDocx(data []byte, index int, alt string) (docxMedia, bool) {
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return docxMedia{}, false
	}
	bounds := decoded.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 {
		return docxMedia{}, false
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, decoded); err != nil {
		return docxMedia{}, false
	}
	widthEMU := int64(bounds.Dx()) * 914400 / 96
	heightEMU := int64(bounds.Dy()) * 914400 / 96
	const maxWidthEMU int64 = 5760000
	const maxHeightEMU int64 = 4114800
	if widthEMU > maxWidthEMU {
		heightEMU = heightEMU * maxWidthEMU / widthEMU
		widthEMU = maxWidthEMU
	}
	if heightEMU > maxHeightEMU {
		widthEMU = widthEMU * maxHeightEMU / heightEMU
		heightEMU = maxHeightEMU
	}
	return docxMedia{
		Name: fmt.Sprintf("image%d.png", index), RelID: fmt.Sprintf("rId%d", index+2), Alt: alt,
		Data: encoded.Bytes(), WidthEMU: widthEMU, HeightEMU: heightEMU,
	}, true
}

func docxImageParagraph(imagePart docxMedia, index int) string {
	alt := xmlText(imagePart.Alt)
	return fmt.Sprintf(`<w:p><w:pPr><w:jc w:val="center"/><w:spacing w:before="120" w:after="120"/></w:pPr><w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/><wp:effectExtent l="0" t="0" r="0" b="0"/><wp:docPr id="%d" name="Image %d" descr="%s"/><wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="0" name="%s" descr="%s"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`, imagePart.WidthEMU, imagePart.HeightEMU, index, index, alt, imagePart.Name, alt, imagePart.RelID, imagePart.WidthEMU, imagePart.HeightEMU)
}

func docxTable(rows []exportLine) string {
	columns := 0
	for _, row := range rows {
		if len(row.Cells) > columns {
			columns = len(row.Cells)
		}
	}
	if columns == 0 {
		return ""
	}
	widths := docxTableWidths(rows, columns, 9240)
	var out strings.Builder
	out.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="9240" w:type="dxa"/><w:tblInd w:w="120" w:type="dxa"/><w:tblLayout w:type="fixed"/><w:tblBorders><w:top w:val="single" w:sz="4" w:color="B7C2BA"/><w:left w:val="single" w:sz="4" w:color="B7C2BA"/><w:bottom w:val="single" w:sz="4" w:color="B7C2BA"/><w:right w:val="single" w:sz="4" w:color="B7C2BA"/><w:insideH w:val="single" w:sz="4" w:color="D8DED9"/><w:insideV w:val="single" w:sz="4" w:color="D8DED9"/></w:tblBorders><w:tblCellMar><w:top w:w="100" w:type="dxa"/><w:left w:w="120" w:type="dxa"/><w:bottom w:w="100" w:type="dxa"/><w:right w:w="120" w:type="dxa"/></w:tblCellMar></w:tblPr><w:tblGrid>`)
	for _, width := range widths {
		fmt.Fprintf(&out, `<w:gridCol w:w="%d"/>`, width)
	}
	out.WriteString(`</w:tblGrid>`)
	for rowIndex, row := range rows {
		header := row.Kind == "tableHeader" || (rowIndex == 0 && rows[0].Kind == "tableHeader")
		out.WriteString(`<w:tr>`)
		if header {
			out.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
		}
		for column := 0; column < columns; column++ {
			cell := ""
			if column < len(row.Cells) {
				cell = row.Cells[column]
			}
			fmt.Fprintf(&out, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>`, widths[column])
			if header {
				out.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="E8F0EA"/>`)
			}
			out.WriteString(`</w:tcPr><w:p><w:pPr><w:spacing w:after="0"/></w:pPr><w:r>`)
			if header {
				out.WriteString(`<w:rPr><w:b/></w:rPr>`)
			}
			fmt.Fprintf(&out, `<w:t xml:space="preserve">%s</w:t></w:r></w:p></w:tc>`, xmlText(cell))
		}
		out.WriteString(`</w:tr>`)
	}
	out.WriteString(`</w:tbl><w:p/>`)
	return out.String()
}

func docxTableWidths(rows []exportLine, columns, total int) []int {
	weights := make([]int, columns)
	for i := range weights {
		weights[i] = 8
	}
	for _, row := range rows {
		for column, cell := range row.Cells {
			if column >= columns {
				break
			}
			length := utf8.RuneCountInString(cell)
			if length > 40 {
				length = 40
			}
			if length > weights[column] {
				weights[column] = length
			}
		}
	}
	weightTotal := 0
	for _, weight := range weights {
		weightTotal += weight
	}
	minimum := 720
	if minimum*columns > total {
		minimum = total / columns
	}
	widths := make([]int, columns)
	remaining := total - minimum*columns
	allocated := 0
	for i, weight := range weights {
		widths[i] = minimum
		if i == columns-1 {
			widths[i] += remaining - allocated
			break
		}
		extra := remaining * weight / weightTotal
		widths[i] += extra
		allocated += extra
	}
	return widths
}

func xmlText(text string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(text))
	return out.String()
}
