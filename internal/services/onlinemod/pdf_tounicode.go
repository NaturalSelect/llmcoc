// NOTE: pdf_tounicode.go 补上底层 PDF 库缺的一块：字体的 /Encoding 是带 /Differences 的字典时，库只按字形名
// 查表，字形名是 gid123 这类无意义名字就原样输出字节（Latin-1 乱码），完全不看 /ToUnicode。
// 而按 PDF 规范 /ToUnicode 才是文字语义的权威映射，pypdf 等库都优先用它。这里解析它并接管这类字体。
package onlinemod

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/ledongthuc/pdf"
)

// maxToUnicodeBytes 限制单个 ToUnicode 流读入的大小，正常字体只有几十 KB。
const maxToUnicodeBytes = 4 << 20

var (
	reCMapBlock = regexp.MustCompile(`(?s)begin(codespacerange|bfchar|bfrange)(.*?)end(?:codespacerange|bfchar|bfrange)`)
	reCMapToken = regexp.MustCompile(`<([0-9A-Fa-f\s]*)>|\[|\]`)
)

type cmapToken struct {
	bytes   string
	bracket byte // '[' 或 ']'，0 表示十六进制串
}

type codespace struct{ lo, hi string }

type bfRange struct {
	lo, hi uint32
	dst    string   // 单值：区间起点对应的 UTF-16BE
	list   []string // 数组形式：逐码点的 UTF-16BE
}

// toUnicodeMap 是解析好的 ToUnicode CMap。
type toUnicodeMap struct {
	spaces []codespace
	chars  map[string]string
	ranges map[int][]bfRange // 按码宽分组，组内按 lo 排序
}

func parseToUnicode(r io.Reader) *toUnicodeMap {
	data, err := io.ReadAll(io.LimitReader(r, maxToUnicodeBytes))
	if err != nil {
		return nil
	}
	m := &toUnicodeMap{chars: map[string]string{}, ranges: map[int][]bfRange{}}
	for _, blk := range reCMapBlock.FindAllSubmatch(data, -1) {
		toks := cmapTokens(string(blk[2]))
		switch string(blk[1]) {
		case "codespacerange":
			for i := 0; i+1 < len(toks); i += 2 {
				lo, hi := toks[i].bytes, toks[i+1].bytes
				if len(lo) > 0 && len(lo) == len(hi) && len(lo) <= 4 {
					m.spaces = append(m.spaces, codespace{lo, hi})
				}
			}
		case "bfchar":
			for i := 0; i+1 < len(toks); i += 2 {
				if toks[i].bracket == 0 && toks[i+1].bracket == 0 && len(toks[i].bytes) > 0 {
					m.chars[toks[i].bytes] = toks[i+1].bytes
				}
			}
		case "bfrange":
			m.addRanges(toks)
		}
	}
	if len(m.chars) == 0 && len(m.ranges) == 0 {
		return nil
	}
	for w := range m.ranges {
		rs := m.ranges[w]
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].lo < rs[j].lo })
	}
	if len(m.spaces) == 0 {
		m.spaces = m.impliedSpaces()
	}
	return m
}

func (m *toUnicodeMap) addRanges(toks []cmapToken) {
	for i := 0; i+2 < len(toks); {
		lo, hi := toks[i], toks[i+1]
		if lo.bracket != 0 || hi.bracket != 0 || len(lo.bytes) == 0 || len(lo.bytes) != len(hi.bytes) || len(lo.bytes) > 4 {
			i++
			continue
		}
		w := len(lo.bytes)
		rg := bfRange{lo: beCode(lo.bytes), hi: beCode(hi.bytes)}
		i += 2
		if toks[i].bracket == '[' {
			i++
			for i < len(toks) && toks[i].bracket != ']' {
				rg.list = append(rg.list, toks[i].bytes)
				i++
			}
			i++
		} else {
			rg.dst = toks[i].bytes
			i++
		}
		if rg.hi >= rg.lo {
			m.ranges[w] = append(m.ranges[w], rg)
		}
	}
}

// impliedSpaces 在 CMap 没写 codespacerange 时，按映射键的宽度补一个覆盖全部取值的码空间。
func (m *toUnicodeMap) impliedSpaces() []codespace {
	widths := map[int]bool{}
	for k := range m.chars {
		widths[len(k)] = true
	}
	for w := range m.ranges {
		widths[w] = true
	}
	var out []codespace
	for w := 1; w <= 4; w++ {
		if widths[w] {
			out = append(out, codespace{strings.Repeat("\x00", w), strings.Repeat("\xff", w)})
		}
	}
	return out
}

func cmapTokens(s string) []cmapToken {
	var out []cmapToken
	for _, m := range reCMapToken.FindAllStringSubmatch(s, -1) {
		switch m[0] {
		case "[", "]":
			out = append(out, cmapToken{bracket: m[0][0]})
		default:
			out = append(out, cmapToken{bytes: hexBytes(m[1])})
		}
	}
	return out
}

func hexBytes(h string) string {
	h = strings.Join(strings.Fields(h), "")
	if len(h)%2 == 1 {
		h += "0"
	}
	var b strings.Builder
	for i := 0; i+1 < len(h); i += 2 {
		var v byte
		fmt.Sscanf(h[i:i+2], "%02x", &v)
		b.WriteByte(v)
	}
	return b.String()
}

func beCode(s string) uint32 {
	var v uint32
	for i := 0; i < len(s); i++ {
		v = v<<8 | uint32(s[i])
	}
	return v
}

func utf16beToString(s string) string {
	if len(s) == 1 {
		return string(rune(s[0]))
	}
	var u []uint16
	for i := 0; i+1 < len(s); i += 2 {
		u = append(u, uint16(s[i])<<8|uint16(s[i+1]))
	}
	return string(utf16.Decode(u))
}

// Decode 把字体的字符码序列还原成文本。码宽由 codespace 决定，查不到的码输出 U+FFFD。
func (m *toUnicodeMap) Decode(raw string) string {
	var sb strings.Builder
	for len(raw) > 0 {
		n := m.codeWidth(raw)
		if n == 0 {
			sb.WriteRune(unicode.ReplacementChar)
			raw = raw[1:]
			continue
		}
		code := raw[:n]
		raw = raw[n:]
		if s, ok := m.lookup(code); ok {
			sb.WriteString(s)
		} else {
			sb.WriteRune(unicode.ReplacementChar)
		}
	}
	return sb.String()
}

func (m *toUnicodeMap) codeWidth(raw string) int {
	for n := 1; n <= 4 && n <= len(raw); n++ {
		for _, sp := range m.spaces {
			if len(sp.lo) != n {
				continue
			}
			ok := true
			for i := 0; i < n; i++ {
				if raw[i] < sp.lo[i] || raw[i] > sp.hi[i] {
					ok = false
					break
				}
			}
			if ok {
				return n
			}
		}
	}
	return 0
}

func (m *toUnicodeMap) lookup(code string) (string, bool) {
	if dst, ok := m.chars[code]; ok {
		return utf16beToString(dst), true
	}
	rs := m.ranges[len(code)]
	c := beCode(code)
	i := sort.Search(len(rs), func(i int) bool { return rs[i].lo > c }) - 1
	if i < 0 || c > rs[i].hi {
		return "", false
	}
	rg := rs[i]
	off := c - rg.lo
	if rg.list != nil {
		if int(off) < len(rg.list) {
			return utf16beToString(rg.list[off]), true
		}
		return "", false
	}
	rn := []rune(utf16beToString(rg.dst))
	if len(rn) == 0 {
		return "", false
	}
	rn[len(rn)-1] += rune(off)
	return string(rn), true
}

// needsToUnicode 判断字体是否要由本文件接管：Encoding 是字典（库只会按字形名查表）且带 ToUnicode 流。
func needsToUnicode(f pdf.Font) bool {
	return f.V.Key("Encoding").Kind() == pdf.Dict && f.V.Key("ToUnicode").Kind() == pdf.Stream
}

func pageNeedsToUnicode(page pdf.Page) bool {
	for _, name := range page.Fonts() {
		if needsToUnicode(page.Font(name)) {
			return true
		}
	}
	return false
}

// toUnicodePageText 复刻库的 Page.GetPlainText 的取文本逻辑，区别只在接管字体的解码。
func toUnicodePageText(page pdf.Page) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			text, err = "", fmt.Errorf("%v", r)
		}
	}()
	contents := page.V.Key("Contents")
	if page.V.IsNull() || contents.Kind() == pdf.Null {
		return "", nil
	}
	var sb bytes.Buffer
	identity := func(s string) string { return s }
	decode := identity
	// NOTE: 同一页里字体会被反复切换，每次都重新解析 ToUnicode 会慢一个数量级；按资源名缓存，作用域只在本页，
	// 因为不同页的同名字体不是同一个字体。
	cache := map[string]func(string) string{}
	fontDecoder := func(name string) func(string) string {
		if d, ok := cache[name]; ok {
			return d
		}
		d := identity
		font := page.Font(name)
		switch {
		case needsToUnicode(font):
			if m := parseToUnicode(font.V.Key("ToUnicode").Reader()); m != nil {
				d = m.Decode
			}
		case font.V.Kind() == pdf.Dict:
			d = font.Encoder().Decode
		}
		cache[name] = d
		return d
	}
	show := func(v pdf.Value) { sb.WriteString(decode(v.RawString())) }
	pdf.Interpret(contents, func(stk *pdf.Stack, op string) {
		args := make([]pdf.Value, stk.Len())
		for i := len(args) - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}
		switch op {
		case "BT", "T*":
			sb.WriteString("\n")
		case "Tf":
			if len(args) != 2 {
				panic("bad Tf")
			}
			decode = fontDecoder(args[0].Name())
		case "Tj", "'":
			if len(args) >= 1 {
				show(args[0])
			}
		case "\"":
			if len(args) >= 3 {
				show(args[2])
			}
		case "TJ":
			if len(args) >= 1 {
				arr := args[0]
				for i := 0; i < arr.Len(); i++ {
					if x := arr.Index(i); x.Kind() == pdf.String {
						show(x)
					}
				}
			}
		}
	})
	return sb.String(), nil
}
