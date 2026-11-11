package launcherbinary

import (
	"debug/pe"
	"encoding/binary"
	"io"
	"unicode/utf16"
)

const (
	rtVersion        = 16
	maxVersionBlock  = 64 << 10
	maxResourceDepth = 3
)

// PEInfo is the version data of a Windows executable.
type PEInfo struct {
	Machine uint16
	// Version is the ProductVersion string; FileVersion must match it when present.
	Version string
}

// ReadPE reads the machine and the version strings of a PE file.
func ReadPE(r io.ReaderAt, size int64) (PEInfo, error) {
	file, err := pe.NewFile(io.NewSectionReader(r, 0, size))
	if err != nil {
		return PEInfo{}, reject("not_pe", "el binario no es un ejecutable de Windows (PE): %v", err)
	}
	defer file.Close()
	var dir pe.DataDirectory
	switch header := file.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		if header.NumberOfRvaAndSizes > pe.IMAGE_DIRECTORY_ENTRY_RESOURCE {
			dir = header.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_RESOURCE]
		}
	case *pe.OptionalHeader32:
		if header.NumberOfRvaAndSizes > pe.IMAGE_DIRECTORY_ENTRY_RESOURCE {
			dir = header.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_RESOURCE]
		}
	}
	info := PEInfo{Machine: file.Machine}
	missing := reject("version_missing", "el ejecutable no tiene recurso de versión (¿build sin PANDORA_RELEASE_VERSION?)")
	if dir.VirtualAddress == 0 || dir.Size == 0 {
		return info, missing
	}
	var rsrc []byte
	var base uint32
	for _, section := range file.Sections {
		if dir.VirtualAddress >= section.VirtualAddress && dir.VirtualAddress < section.VirtualAddress+max32(section.VirtualSize, section.Size) {
			data, err := section.Data()
			if err != nil {
				return info, reject("not_pe", "no se pudo leer la sección de recursos: %v", err)
			}
			rsrc, base = data, section.VirtualAddress
			break
		}
	}
	if rsrc == nil {
		return info, missing
	}
	start := dir.VirtualAddress - base
	block, ok := findVersionResource(rsrc, start, base)
	if !ok {
		return info, missing
	}
	strings := versionStrings(block)
	product, file2 := strings["ProductVersion"], strings["FileVersion"]
	if product == "" {
		return info, missing
	}
	if file2 != "" && file2 != product {
		return info, reject("version_conflict", "ProductVersion %q y FileVersion %q no coinciden", product, file2)
	}
	info.Version = product
	return info, nil
}

func max32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func u16(b []byte, at uint32) (uint16, bool) {
	if uint64(at)+2 > uint64(len(b)) {
		return 0, false
	}
	return binary.LittleEndian.Uint16(b[at:]), true
}

func u32(b []byte, at uint32) (uint32, bool) {
	if uint64(at)+4 > uint64(len(b)) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b[at:]), true
}

// findVersionResource walks type → name → language and returns the first
// RT_VERSION data block. Offsets inside the tree are relative to its root.
func findVersionResource(rsrc []byte, root, rva uint32) ([]byte, bool) {
	entry := func(dir uint32, wantID int) (uint32, bool) {
		named, ok1 := u16(rsrc, root+dir+12)
		ids, ok2 := u16(rsrc, root+dir+14)
		if !ok1 || !ok2 {
			return 0, false
		}
		for i := uint32(0); i < uint32(named)+uint32(ids); i++ {
			at := root + dir + 16 + i*8
			name, ok1 := u32(rsrc, at)
			target, ok2 := u32(rsrc, at+4)
			if !ok1 || !ok2 {
				return 0, false
			}
			if wantID < 0 || (name&0x80000000 == 0 && int(name) == wantID) {
				return target, true
			}
		}
		return 0, false
	}
	offset := uint32(0)
	for level, want := range []int{rtVersion, -1, -1} {
		target, ok := entry(offset, want)
		if !ok {
			return nil, false
		}
		isDir := target&0x80000000 != 0
		if level < maxResourceDepth-1 {
			if !isDir {
				return nil, false
			}
			offset = target &^ 0x80000000
			continue
		}
		if isDir {
			return nil, false
		}
		dataRVA, ok1 := u32(rsrc, root+target)
		size, ok2 := u32(rsrc, root+target+4)
		if !ok1 || !ok2 || dataRVA < rva || size > maxVersionBlock {
			return nil, false
		}
		at := dataRVA - rva
		if uint64(at)+uint64(size) > uint64(len(rsrc)) {
			return nil, false
		}
		return rsrc[at : at+size], true
	}
	return nil, false
}

// versionStrings collects the String entries of VS_VERSIONINFO →
// StringFileInfo → StringTable (first table wins for each key).
func versionStrings(block []byte) map[string]string {
	out := map[string]string{}
	var walk func(b []byte, depth int)
	walk = func(b []byte, depth int) {
		for len(b) >= 6 && depth <= 3 {
			length, _ := u16(b, 0)
			valueLength, _ := u16(b, 2)
			valueType, _ := u16(b, 4)
			if int(length) < 6 || int(length) > len(b) {
				return
			}
			node := b[:length]
			key, after := utf16z(node, 6)
			at := align4(after)
			valueBytes := int(valueLength)
			if valueType == 1 {
				valueBytes *= 2
			}
			switch {
			case depth == 3 && valueType == 1:
				if at+valueBytes <= len(node) {
					if _, exists := out[key]; !exists {
						value, _ := utf16z(node[:at+valueBytes], at)
						out[key] = value
					}
				}
			default:
				children := align4(at + valueBytes)
				if children < len(node) && (depth != 1 || key == "StringFileInfo") {
					walk(node[children:], depth+1)
				}
			}
			next := align4(int(length))
			if next >= len(b) {
				return
			}
			b = b[next:]
		}
	}
	walk(block, 0)
	return out
}

func align4(n int) int { return (n + 3) &^ 3 }

// utf16z decodes a NUL-terminated UTF-16LE string starting at at and returns it
// with the offset just past the terminator.
func utf16z(b []byte, at int) (string, int) {
	var units []uint16
	for at+1 < len(b) {
		unit := binary.LittleEndian.Uint16(b[at:])
		at += 2
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return string(utf16.Decode(units)), at
}
