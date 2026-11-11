package launcherbinary

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"unicode/utf16"
)

// Synthetic binaries for tests. They are tiny files built in memory with just
// the structures this package reads; no real launcher binary is stored in the
// (public) repository.

// SyntheticPE returns a minimal PE32+ file for machine with a version resource
// holding the given strings (none when strings is nil).
func SyntheticPE(machine uint16, strings map[string]string) []byte {
	const sectionRVA, fileAlign, rawOffset = 0x1000, 0x200, 0x200
	var rsrc []byte
	if strings != nil {
		rsrc = syntheticResourceTree(sectionRVA, versionInfoBlock(strings))
	}
	rawSize := (len(rsrc) + fileAlign - 1) / fileAlign * fileAlign
	if rawSize == 0 {
		rawSize = fileAlign
	}
	var b bytes.Buffer
	le := func(v interface{}) { _ = binary.Write(&b, binary.LittleEndian, v) }
	dos := make([]byte, 0x40)
	copy(dos, "MZ")
	binary.LittleEndian.PutUint32(dos[0x3c:], 0x40)
	b.Write(dos)
	b.WriteString("PE\x00\x00")
	le(machine)
	le(uint16(1))     // NumberOfSections
	le(uint32(0))     // TimeDateStamp
	le(uint32(0))     // PointerToSymbolTable
	le(uint32(0))     // NumberOfSymbols
	le(uint16(240))   // SizeOfOptionalHeader (PE32+)
	le(uint16(0x22))  // Characteristics: executable, large address aware
	le(uint16(0x20b)) // Magic PE32+
	le([2]uint8{14, 0})
	le([3]uint32{uint32(rawSize), 0, 0}) // code, init, uninit sizes
	le(uint32(0))                        // AddressOfEntryPoint
	le(uint32(sectionRVA))               // BaseOfCode
	le(uint64(0x140000000))              // ImageBase
	le(uint32(0x1000))                   // SectionAlignment
	le(uint32(fileAlign))
	le([6]uint16{6, 0, 0, 0, 6, 0})
	le(uint32(0))                   // Win32VersionValue
	le(uint32(sectionRVA + 0x1000)) // SizeOfImage
	le(uint32(rawOffset))           // SizeOfHeaders
	le(uint32(0))                   // CheckSum
	le(uint16(2))                   // Subsystem GUI
	le(uint16(0))                   // DllCharacteristics
	le([4]uint64{0x100000, 0x1000, 0x100000, 0x1000})
	le(uint32(0))  // LoaderFlags
	le(uint32(16)) // NumberOfRvaAndSizes
	for i := 0; i < 16; i++ {
		if i == 2 && rsrc != nil {
			le([2]uint32{sectionRVA, uint32(len(rsrc))})
		} else {
			le([2]uint32{0, 0})
		}
	}
	name := [8]byte{'.', 'r', 's', 'r', 'c'}
	le(name)
	le(uint32(0x1000)) // VirtualSize
	le(uint32(sectionRVA))
	le(uint32(rawSize))
	le(uint32(rawOffset))
	le([3]uint32{0, 0, 0})
	le(uint32(0x40000040)) // initialized data, readable
	for b.Len() < rawOffset {
		b.WriteByte(0)
	}
	section := make([]byte, rawSize)
	copy(section, rsrc)
	b.Write(section)
	return b.Bytes()
}

func utf16zBytes(s string) []byte {
	units := append(utf16.Encode([]rune(s)), 0)
	out := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[2*i:], u)
	}
	return out
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// versionNode encodes one VS_VERSIONINFO-style block.
func versionNode(key string, valueType uint16, value []byte, valueLength uint16, children ...[]byte) []byte {
	body := make([]byte, 6)
	body = append(body, utf16zBytes(key)...)
	body = pad4(body)
	body = append(body, value...)
	for _, child := range children {
		body = pad4(body)
		body = append(body, child...)
	}
	binary.LittleEndian.PutUint16(body[0:], uint16(len(body)))
	binary.LittleEndian.PutUint16(body[2:], valueLength)
	binary.LittleEndian.PutUint16(body[4:], valueType)
	return body
}

func versionInfoBlock(strings map[string]string) []byte {
	var entries [][]byte
	for _, key := range []string{"ProductName", "ProductVersion", "FileVersion", "OriginalFilename"} {
		if value, ok := strings[key]; ok {
			encoded := utf16zBytes(value)
			entries = append(entries, versionNode(key, 1, encoded, uint16(len(encoded)/2)))
		}
	}
	table := versionNode("040904b0", 1, nil, 0, entries...)
	fileInfo := versionNode("StringFileInfo", 1, nil, 0, table)
	fixed := make([]byte, 52)
	binary.LittleEndian.PutUint32(fixed, 0xfeef04bd)
	return versionNode("VS_VERSION_INFO", 0, fixed, uint16(len(fixed)), fileInfo)
}

// syntheticResourceTree builds type(16) → id 1 → language 0x409 → data.
func syntheticResourceTree(rva uint32, data []byte) []byte {
	dir := func(entryName, target uint32) []byte {
		out := make([]byte, 24)
		binary.LittleEndian.PutUint16(out[14:], 1)
		binary.LittleEndian.PutUint32(out[16:], entryName)
		binary.LittleEndian.PutUint32(out[20:], target)
		return out
	}
	var tree []byte
	tree = append(tree, dir(16, 0x80000000|24)...)
	tree = append(tree, dir(1, 0x80000000|48)...)
	tree = append(tree, dir(0x409, 72)...)
	entry := make([]byte, 16)
	binary.LittleEndian.PutUint32(entry, rva+88)
	binary.LittleEndian.PutUint32(entry[4:], uint32(len(data)))
	tree = append(tree, entry...)
	return append(tree, data...)
}

// SyntheticAPK returns a ZIP holding a binary AndroidManifest.xml with the
// given package, versionName and versionCode (an empty versionName omits it)
// and payload as classes.dex, so tests can tell binaries apart.
func SyntheticAPK(pkg, versionName string, versionCode uint32, payload ...byte) []byte {
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	w, _ := archive.Create("AndroidManifest.xml")
	_, _ = w.Write(SyntheticAXML(pkg, versionName, versionCode))
	w2, _ := archive.Create("classes.dex")
	_, _ = w2.Write(append([]byte("dex\n035\x00"), payload...))
	_ = archive.Close()
	return out.Bytes()
}

// SyntheticAXML encodes <manifest package versionCode versionName> in the
// binary XML format (UTF-16 string pool, resource map, one start element).
func SyntheticAXML(pkg, versionName string, versionCode uint32) []byte {
	pool := []string{"versionCode", "versionName", "package", "manifest", "android", "http://schemas.android.com/apk/res/android", pkg, versionName}
	chunk := func(kind, header uint16, body []byte) []byte {
		out := make([]byte, 8, 8+len(body))
		binary.LittleEndian.PutUint16(out, kind)
		binary.LittleEndian.PutUint16(out[2:], header)
		binary.LittleEndian.PutUint32(out[4:], uint32(8+len(body)))
		return append(out, body...)
	}
	var data []byte
	offsets := make([]byte, 4*len(pool))
	for i, s := range pool {
		binary.LittleEndian.PutUint32(offsets[4*i:], uint32(len(data)))
		units := utf16.Encode([]rune(s))
		entry := make([]byte, 2+2*len(units)+2)
		binary.LittleEndian.PutUint16(entry, uint16(len(units)))
		for j, u := range units {
			binary.LittleEndian.PutUint16(entry[2+2*j:], u)
		}
		data = append(data, entry...)
	}
	data = pad4(data)
	poolHeader := make([]byte, 20)
	binary.LittleEndian.PutUint32(poolHeader, uint32(len(pool)))
	binary.LittleEndian.PutUint32(poolHeader[12:], uint32(28+len(offsets)))
	stringPool := chunk(0x0001, 28, append(append(poolHeader, offsets...), data...))
	resMap := make([]byte, 8)
	binary.LittleEndian.PutUint32(resMap, attrVersionCode)
	binary.LittleEndian.PutUint32(resMap[4:], attrVersionName)
	resourceMap := chunk(0x0180, 8, resMap)
	attr := func(ns, name, raw uint32, dataType uint8, value uint32) []byte {
		out := make([]byte, 20)
		binary.LittleEndian.PutUint32(out, ns)
		binary.LittleEndian.PutUint32(out[4:], name)
		binary.LittleEndian.PutUint32(out[8:], raw)
		binary.LittleEndian.PutUint16(out[12:], 8)
		out[15] = dataType
		binary.LittleEndian.PutUint32(out[16:], value)
		return out
	}
	attrs := [][]byte{attr(5, 0, 0xffffffff, typeIntDec, versionCode)}
	if versionName != "" {
		attrs = append(attrs, attr(5, 1, 7, typeString, 7))
	}
	attrs = append(attrs, attr(0xffffffff, 2, 6, typeString, 6))
	element := make([]byte, 8+20)
	binary.LittleEndian.PutUint32(element, 1)              // line
	binary.LittleEndian.PutUint32(element[4:], 0xffffffff) // comment
	binary.LittleEndian.PutUint32(element[8:], 0xffffffff) // ns
	binary.LittleEndian.PutUint32(element[12:], 3)         // name
	binary.LittleEndian.PutUint16(element[16:], 20)        // attributeStart
	binary.LittleEndian.PutUint16(element[18:], 20)        // attributeSize
	binary.LittleEndian.PutUint16(element[20:], uint16(len(attrs)))
	for _, a := range attrs {
		element = append(element, a...)
	}
	start := chunk(0x0102, 16, element)
	body := append(append(stringPool, resourceMap...), start...)
	return chunk(0x0003, 8, body)
}
