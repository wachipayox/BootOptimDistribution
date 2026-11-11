package launcherbinary

import (
	"archive/zip"
	"encoding/binary"
	"io"
	"unicode/utf16"
)

const (
	maxManifestBytes = 4 << 20

	axmlFile        = 0x0003
	axmlStringPool  = 0x0001
	axmlResourceMap = 0x0180
	axmlStartElem   = 0x0102

	attrVersionCode = 0x0101021b
	attrVersionName = 0x0101021c

	typeReference = 0x01
	typeString    = 0x03
	typeIntDec    = 0x10
	typeIntHex    = 0x11
)

// APKInfo is what the Android updater compares before installing.
type APKInfo struct {
	Package     string
	VersionName string
	VersionCode int64
}

// ReadAPK reads AndroidManifest.xml from an APK (a ZIP file).
func ReadAPK(r io.ReaderAt, size int64) (APKInfo, error) {
	archive, err := zip.NewReader(r, size)
	if err != nil {
		return APKInfo{}, reject("not_apk", "el binario no es un APK (ZIP): %v", err)
	}
	for _, file := range archive.File {
		if file.Name != "AndroidManifest.xml" {
			continue
		}
		if file.UncompressedSize64 > maxManifestBytes {
			return APKInfo{}, reject("not_apk", "AndroidManifest.xml es demasiado grande")
		}
		reader, err := file.Open()
		if err != nil {
			return APKInfo{}, reject("not_apk", "no se pudo abrir AndroidManifest.xml: %v", err)
		}
		data, err := io.ReadAll(io.LimitReader(reader, maxManifestBytes+1))
		reader.Close()
		if err != nil || len(data) > maxManifestBytes {
			return APKInfo{}, reject("not_apk", "no se pudo leer AndroidManifest.xml")
		}
		return parseAXMLManifest(data)
	}
	return APKInfo{}, reject("not_apk", "el APK no contiene AndroidManifest.xml")
}

func parseAXMLManifest(b []byte) (APKInfo, error) {
	bad := reject("bad_manifest", "AndroidManifest.xml binario no válido")
	if t, ok := u16(b, 0); !ok || t != axmlFile {
		return APKInfo{}, bad
	}
	headerSize, _ := u16(b, 2)
	var pool []string
	var resIDs []uint32
	for at := uint32(headerSize); at+8 <= uint32(len(b)); {
		chunkType, _ := u16(b, at)
		chunkHeader, _ := u16(b, at+2)
		chunkSize, _ := u32(b, at+4)
		if chunkSize < 8 || uint64(at)+uint64(chunkSize) > uint64(len(b)) {
			return APKInfo{}, bad
		}
		chunk := b[at : at+chunkSize]
		switch chunkType {
		case axmlStringPool:
			var ok bool
			if pool, ok = parseStringPool(chunk); !ok {
				return APKInfo{}, bad
			}
		case axmlResourceMap:
			for i := uint32(chunkHeader); i+4 <= chunkSize; i += 4 {
				id, _ := u32(chunk, i)
				resIDs = append(resIDs, id)
			}
		case axmlStartElem:
			return manifestElement(chunk, uint32(chunkHeader), pool, resIDs)
		}
		at += chunkSize
	}
	return APKInfo{}, bad
}

func manifestElement(chunk []byte, header uint32, pool []string, resIDs []uint32) (APKInfo, error) {
	bad := reject("bad_manifest", "AndroidManifest.xml binario no válido")
	str := func(i uint32) (string, bool) {
		if i == 0xffffffff || uint64(i) >= uint64(len(pool)) {
			return "", false
		}
		return pool[i], true
	}
	name, _ := u32(chunk, header+4)
	if s, ok := str(name); !ok || s != "manifest" {
		return APKInfo{}, reject("bad_manifest", "el primer elemento de AndroidManifest.xml no es <manifest>")
	}
	attrStart, ok1 := u16(chunk, header+8)
	attrSize, ok2 := u16(chunk, header+10)
	attrCount, ok3 := u16(chunk, header+12)
	if !ok1 || !ok2 || !ok3 || attrSize < 20 {
		return APKInfo{}, bad
	}
	var info APKInfo
	var haveName, haveCode bool
	for i := uint32(0); i < uint32(attrCount); i++ {
		at := header + uint32(attrStart) + i*uint32(attrSize)
		if uint64(at)+20 > uint64(len(chunk)) {
			return APKInfo{}, bad
		}
		nameIndex, _ := u32(chunk, at+4)
		raw, _ := u32(chunk, at+8)
		dataType := chunk[at+15]
		data, _ := u32(chunk, at+16)
		attrName, _ := str(nameIndex)
		var resID uint32
		if uint64(nameIndex) < uint64(len(resIDs)) {
			resID = resIDs[nameIndex]
		}
		text := func() (string, bool) {
			if dataType == typeString {
				return str(data)
			}
			return str(raw)
		}
		switch {
		case resID == attrVersionName || (resID == 0 && attrName == "versionName"):
			if dataType == typeReference {
				return APKInfo{}, reject("version_reference", "versionName es una referencia a un recurso; debe ser texto literal")
			}
			if info.VersionName, haveName = text(); !haveName {
				return APKInfo{}, bad
			}
		case resID == attrVersionCode || (resID == 0 && attrName == "versionCode"):
			if dataType != typeIntDec && dataType != typeIntHex {
				return APKInfo{}, reject("bad_manifest", "versionCode no es un entero")
			}
			info.VersionCode, haveCode = int64(data), true
		case resID == 0 && attrName == "package":
			info.Package, _ = text()
		}
	}
	if !haveName || info.VersionName == "" {
		return APKInfo{}, reject("version_missing", "el APK no declara versionName")
	}
	if !haveCode {
		return APKInfo{}, reject("version_missing", "el APK no declara versionCode")
	}
	return info, nil
}

func parseStringPool(chunk []byte) ([]string, bool) {
	count, ok1 := u32(chunk, 8)
	flags, ok2 := u32(chunk, 16)
	stringsStart, ok3 := u32(chunk, 20)
	header, ok4 := u16(chunk, 2)
	if !ok1 || !ok2 || !ok3 || !ok4 || count > 1<<20 {
		return nil, false
	}
	utf8 := flags&0x100 != 0
	out := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		offset, ok := u32(chunk, uint32(header)+i*4)
		if !ok {
			return nil, false
		}
		at := uint64(stringsStart) + uint64(offset)
		if at >= uint64(len(chunk)) {
			return nil, false
		}
		s, ok := decodePoolString(chunk[at:], utf8)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func decodePoolString(b []byte, utf8 bool) (string, bool) {
	if utf8 {
		// UTF-16 length then UTF-8 length, each 1 or 2 bytes.
		at := 0
		lengths := [2]int{}
		for k := range lengths {
			if at >= len(b) {
				return "", false
			}
			n := int(b[at])
			at++
			if n&0x80 != 0 {
				if at >= len(b) {
					return "", false
				}
				n = (n&0x7f)<<8 | int(b[at])
				at++
			}
			lengths[k] = n
		}
		if at+lengths[1] > len(b) {
			return "", false
		}
		return string(b[at : at+lengths[1]]), true
	}
	if len(b) < 2 {
		return "", false
	}
	n := int(binary.LittleEndian.Uint16(b))
	at := 2
	if n&0x8000 != 0 {
		if len(b) < 4 {
			return "", false
		}
		n = (n&0x7fff)<<16 | int(binary.LittleEndian.Uint16(b[2:]))
		at = 4
	}
	if at+2*n > len(b) {
		return "", false
	}
	units := make([]uint16, n)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(b[at+2*i:])
	}
	return string(utf16.Decode(units)), true
}
