// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import "fmt"

/* RFC 7932 121-Word Transformation Matrix Tables */

var kRFC7932PrefixSuffix = []byte{
0x01,0x20,0x02,0x2C,0x20,0x08,0x20,0x6F,0x66,0x20,0x74,0x68,0x65,0x20,0x04,0x20,
0x6F,0x66,0x20,0x02,0x73,0x20,0x01,0x2E,0x05,0x20,0x61,0x6E,0x64,0x20,0x04,0x20,
0x69,0x6E,0x20,0x01,0x22,0x04,0x20,0x74,0x6F,0x20,0x02,0x22,0x3E,0x01,0x0A,0x02,
0x2E,0x20,0x01,0x5D,0x05,0x20,0x66,0x6F,0x72,0x20,0x03,0x20,0x61,0x20,0x06,0x20,
0x74,0x68,0x61,0x74,0x20,0x01,0x27,0x06,0x20,0x77,0x69,0x74,0x68,0x20,0x06,0x20,
0x66,0x72,0x6F,0x6D,0x20,0x04,0x20,0x62,0x79,0x20,0x01,0x28,0x06,0x2E,0x20,0x54,
0x68,0x65,0x20,0x04,0x20,0x6F,0x6E,0x20,0x04,0x20,0x61,0x73,0x20,0x04,0x20,0x69,
0x73,0x20,0x04,0x69,0x6E,0x67,0x20,0x02,0x0A,0x09,0x01,0x3A,0x03,0x65,0x64,0x20,
0x02,0x3D,0x22,0x04,0x20,0x61,0x74,0x20,0x03,0x6C,0x79,0x20,0x01,0x2C,0x02,0x3D,
0x27,0x05,0x2E,0x63,0x6F,0x6D,0x2F,0x07,0x2E,0x20,0x54,0x68,0x69,0x73,0x20,0x05,
0x20,0x6E,0x6F,0x74,0x20,0x03,0x65,0x72,0x20,0x03,0x61,0x6C,0x20,0x04,0x66,0x75,
0x6C,0x20,0x04,0x69,0x76,0x65,0x20,0x05,0x6C,0x65,0x73,0x73,0x20,0x04,0x65,0x73,
0x74,0x20,0x04,0x69,0x7A,0x65,0x20,0x02,0xC2,0xA0,0x04,0x6F,0x75,0x73,0x20,0x05,
0x20,0x74,0x68,0x65,0x20,0x02,0x65,0x20,0x00,
}

var kRFC7932PrefixSuffixMap = [50]uint16{
0x0000,0x0002,0x0005,0x000E,0x0013,0x0016,0x0018,0x001E,0x0023,0x0025,
0x002A,0x002D,0x002F,0x0032,0x0034,0x003A,0x003E,0x0045,0x0047,0x004E,
0x0055,0x005A,0x005C,0x0063,0x0068,0x006D,0x0072,0x0077,0x007A,0x007C,
0x0080,0x0083,0x0088,0x008C,0x008E,0x0091,0x0097,0x009F,0x00A5,0x00A9,
0x00AD,0x00B2,0x00B7,0x00BD,0x00C2,0x00C7,0x00CA,0x00CF,0x00D5,0x00D8,

}

var kRFC7932Transforms = [363]uint8{
49,0,49,49,0,0,0,0,0,49,12,49,49,10,0,
49,0,47,0,0,49,4,0,0,49,0,3,49,10,49,
49,0,6,49,13,49,49,1,49,1,0,0,49,0,1,
0,10,0,49,0,7,49,0,9,48,0,0,49,0,8,
49,0,5,49,0,10,49,0,11,49,3,49,49,0,13,
49,0,14,49,14,49,49,2,49,49,0,15,49,0,16,
0,10,49,49,0,12,5,0,49,0,0,1,49,15,49,
49,0,18,49,0,17,49,0,19,49,0,20,49,16,49,
49,17,49,47,0,49,49,4,49,49,0,22,49,11,49,
49,0,23,49,0,24,49,0,25,49,7,49,49,1,26,
49,0,27,49,0,28,0,0,12,49,0,29,49,20,49,
49,18,49,49,6,49,49,0,21,49,10,1,49,8,49,
49,0,31,49,0,32,47,0,3,49,5,49,49,9,49,
0,10,1,49,10,8,5,0,21,49,11,0,49,10,10,
49,0,30,0,0,5,35,0,49,47,0,2,49,10,17,
49,0,36,49,0,33,5,0,0,49,10,21,49,10,5,
49,0,37,0,0,30,49,0,38,0,11,0,49,0,39,
0,11,49,49,0,34,49,11,8,49,10,12,0,0,21,
49,0,40,0,10,12,49,0,41,49,0,42,49,11,17,
49,0,43,0,10,5,49,11,10,0,0,34,49,10,33,
49,0,44,49,11,5,45,0,49,0,0,33,49,10,30,
49,11,30,49,0,46,49,11,1,49,10,34,0,10,33,
0,11,30,0,11,1,49,11,33,49,11,21,49,11,12,
0,11,5,49,11,34,0,11,12,0,10,30,0,11,34,
0,10,34,
}

// rfcTopWords contains 512 high-frequency domain vocabulary words.
var rfcTopWords = []string{
	"return", // [0]
	"mathcal", // [1]
	"uint8_t", // [2]
	"size_t", // [3]
	"background", // [4]
	"mathbb", // [5]
	"compression", // [6]
	"decompression", // [7]
	"mathbf", // [8]
	"dst_pos", // [9]
	"literal_pct", // [10]
	"text", // [11]
	"errcorruptedframe", // [12]
	"orig_len", // [13]
	"mathematical", // [14]
	"aes_distinguisher", // [15]
	"repeat_pct", // [16]
	"bytes", // [17]
	"read_u32_le", // [18]
	"section", // [19]
	"throughput", // [20]
	"src_len", // [21]
	"uint32_t", // [22]
	"match_pct", // [23]
	"decompressor", // [24]
	"timeline", // [25]
	"entropy", // [26]
	"dictionary", // [27]
	"allocations", // [28]
	"minmatchbase", // [29]
	"byte", // [30]
	"vector", // [31]
	"class", // [32]
	"color", // [33]
	"dstpos", // [34]
	"stream", // [35]
	"discrete", // [36]
	"match", // [37]
	"strictly", // [38]
	"tooltip", // [39]
	"style", // [40]
	"coordinate", // [41]
	"execution", // [42]
	"uncompressed", // [43]
	"offset", // [44]
	"payload", // [45]
	"memory", // [46]
	"architecture", // [47]
	"implementation", // [48]
	"przm", // [49]
	"standard", // [50]
	"multiplication", // [51]
	"codingprojects", // [52]
	"theorem", // [53]
	"accent", // [54]
	"specification", // [55]
	"transformation", // [56]
	"length", // [57]
	"instruction", // [58]
	"deterministic", // [59]
	"compressed", // [60]
	"gf256_mul", // [61]
	"across", // [62]
	"invariant", // [63]
	"information", // [64]
	"verification", // [65]
	"baselen", // [66]
	"contracted_len", // [67]
	"bigoplus_", // [68]
	"algebraic", // [69]
	"decode_dst", // [70]
	"times", // [71]
	"empirical", // [72]
	"latency", // [73]
	"directory", // [74]
	"przm_safety_padding", // [75]
	"active", // [76]
	"continue", // [77]
	"przm_max_huff_bits", // [78]
	"instantaneous", // [79]
	"distribution", // [80]
	"przm_gpu_experiments", // [81]
	"parallel", // [82]
	"total_literals", // [83]
	"matches", // [84]
	"int64_t", // [85]
	"allocation", // [86]
	"architectural", // [87]
	"operations", // [88]
	"purple", // [89]
	"matrix", // [90]
	"cycles", // [91]
	"register", // [92]
	"percentage", // [93]
	"transform", // [94]
	"przm_check_cap", // [95]
	"tls_archival_scratch", // [96]
	"literal", // [97]
	"huff_reader_consume", // [98]
	"theoretical", // [99]
	"przm_huff_entry_t", // [100]
	"differential", // [101]
	"header", // [102]
	"dimensional", // [103]
	"permutation", // [104]
	"oplus", // [105]
	"uint16_t", // [106]
	"tls_split_scratch", // [107]
	"kernel", // [108]
	"przm_max_block_size", // [109]
	"cryptographic", // [110]
	"total_matches", // [111]
	"alloc_size", // [112]
	"reconstruction", // [113]
	"with", // [114]
	"without", // [115]
	"distance", // [116]
	"litlen", // [117]
	"data", // [118]
	"allocs", // [119]
	"verified", // [120]
	"symplectic", // [121]
	"state", // [122]
	"uint64", // [123]
	"cmdbuf", // [124]
	"start", // [125]
	"instructions", // [126]
	"formulation", // [127]
	"mixcolumns", // [128]
	"stride", // [129]
	"binary", // [130]
	"unsafe", // [131]
	"token", // [132]
	"const", // [133]
	"expansion", // [134]
	"table", // [135]
	"registers", // [136]
	"benchmark", // [137]
	"extended", // [138]
	"hardware", // [139]
	"structured", // [140]
	"huff_reader_peek", // [141]
	"context", // [142]
	"block", // [143]
	"huff_entry_codelen", // [144]
	"border", // [145]
	"dynamic", // [146]
	"dst_cap", // [147]
	"target", // [148]
	"litptr", // [149]
	"memcpy", // [150]
	"gpu_archival_report", // [151]
	"payload_len", // [152]
	"linear", // [153]
	"huff_entry_symbol", // [154]
	"cryptanalysis", // [155]
	"zero", // [156]
	"column", // [157]
	"topological", // [158]
	"interleaved", // [159]
	"coordinates", // [160]
	"independent", // [161]
	"opcode", // [162]
	"windowlog", // [163]
	"round", // [164]
	"tropical", // [165]
	"literalbuf", // [166]
	"delta", // [167]
	"description", // [168]
	"repcode", // [169]
	"pointer", // [170]
	"przm_inline", // [171]
	"outbuf", // [172]
	"przm_decompress", // [173]
	"polynomial", // [174]
	"function", // [175]
	"orig_size", // [176]
	"streaming", // [177]
	"uint32", // [178]
	"vgf2p8affineqb", // [179]
	"analysis", // [180]
	"truncated", // [181]
	"przm_ok", // [182]
	"crossover", // [183]
	"lit_len", // [184]
	"cmdptr", // [185]
	"ultrametric", // [186]
	"grammar", // [187]
	"exact", // [188]
	"executes", // [189]
	"reference", // [190]
	"boundary", // [191]
	"errorf", // [192]
	"pipeline", // [193]
	"reduction", // [194]
	"integration", // [195]
	"cmd_ptr", // [196]
	"original_request", // [197]
	"littleendian", // [198]
	"static", // [199]
	"signature", // [200]
	"complexity", // [201]
	"structural", // [202]
	"directly", // [203]
	"where", // [204]
	"fusedext", // [205]
	"frame", // [206]
	"gpu_bench_test", // [207]
	"acceleration", // [208]
	"mathrm", // [209]
	"decompressed", // [210]
	"https", // [211]
	"branchless", // [212]
	"przm_expand_base64", // [213]
	"uint64_t", // [214]
	"sequence", // [215]
	"single", // [216]
	"archivestartoffset", // [217]
	"evaluates", // [218]
	"algorithm", // [219]
	"dec_table", // [220]
	"requires", // [221]
	"cachelinesize", // [222]
	"property", // [223]
	"template", // [224]
	"encryption", // [225]
	"else", // [226]
	"basedst", // [227]
	"inverse", // [228]
	"dedupblocksize", // [229]
	"bitstream", // [230]
	"secondary", // [231]
	"over", // [232]
	"repeat", // [233]
	"cellular", // [234]
	"lit_ptr", // [235]
	"level", // [236]
	"because", // [237]
	"identically", // [238]
	"chunk_size", // [239]
	"fast_read_varint", // [240]
	"contiguous", // [241]
	"identical", // [242]
	"geometric", // [243]
	"engine", // [244]
	"int32", // [245]
	"chunk", // [246]
	"streams", // [247]
	"bounded", // [248]
	"shannon", // [249]
	"structures", // [250]
	"degree", // [251]
	"annihilation", // [252]
	"alignedbytes", // [253]
	"galois", // [254]
	"litstart", // [255]
	"cryptanalytic", // [256]
	"distinguisher", // [257]
	"get_schema_token", // [258]
	"synthesis", // [259]
	"blacksquare", // [260]
	"arithmetic", // [261]
	"integrity", // [262]
	"immediately", // [263]
	"compressoptions", // [264]
	"vgf2p8mulb", // [265]
	"read_u16_le", // [266]
	"integer", // [267]
	"parameters", // [268]
	"field", // [269]
	"size", // [270]
	"uint8", // [271]
	"extra", // [272]
	"append", // [273]
	"fastreadvarint", // [274]
	"isoquant", // [275]
	"lossless", // [276]
	"lengths", // [277]
	"framedecoder", // [278]
	"inversion", // [279]
	"offsetbuf", // [280]
	"classical", // [281]
	"prefix", // [282]
	"canonical", // [283]
	"windowsize", // [284]
	"overhead", // [285]
	"mapping", // [286]
	"exactly", // [287]
	"probability", // [288]
	"uintptr", // [289]
	"direct", // [290]
	"uint16", // [291]
	"from", // [292]
	"uncompressedsize", // [293]
	"tls_norm_scratch", // [294]
	"centraldiroffset", // [295]
	"container", // [296]
	"machine", // [297]
	"norm_mode", // [298]
	"shiftrows", // [299]
	"mlen", // [300]
	"measured", // [301]
	"archival", // [302]
	"division", // [303]
	"culaunchkernel", // [304]
	"min_match_base", // [305]
	"characteristic", // [306]
	"identifier", // [307]
	"window", // [308]
	"statistical", // [309]
	"error", // [310]
	"pmatrix", // [311]
	"into", // [312]
	"vectors", // [313]
	"padding", // [314]
	"flags", // [315]
	"explicit", // [316]
	"reserved", // [317]
	"decomposition", // [318]
	"fused", // [319]
	"right", // [320]
	"sheaf", // [321]
	"documentation", // [322]
	"uint32_le", // [323]
	"family", // [324]
	"count", // [325]
	"number", // [326]
	"lemon4ksan", // [327]
	"chunkindex", // [328]
	"evaluating", // [329]
	"sequential", // [330]
	"constant", // [331]
	"przm_dec", // [332]
	"span", // [333]
	"transformed", // [334]
	"output", // [335]
	"optimal", // [336]
	"satisfies", // [337]
	"width", // [338]
	"przm_err_offset_oob", // [339]
	"hierarchical", // [340]
	"offsets", // [341]
	"hasstride", // [342]
	"initpadsize", // [343]
	"bandwidth", // [344]
	"mathematically", // [345]
	"huff_reader_init", // [346]
	"sigma", // [347]
	"space", // [348]
	"multi", // [349]
	"bits", // [350]
	"symbol", // [351]
	"cell", // [352]
	"default", // [353]
	"continuous", // [354]
	"decoding", // [355]
	"trellis", // [356]
	"bstart", // [357]
	"candcount", // [358]
	"this", // [359]
	"microcode", // [360]
	"command", // [361]
	"proof", // [362]
	"defined", // [363]
	"float64", // [364]
	"collimation", // [365]
	"woffset", // [366]
	"channel", // [367]
	"magic", // [368]
	"overflow", // [369]
	"diagonal", // [370]
	"harmonic", // [371]
	"balanced", // [372]
	"theorems", // [373]
	"lambda", // [374]
	"physical", // [375]
	"frac", // [376]
	"dppad", // [377]
	"rep0", // [378]
	"difference", // [379]
	"evaluation", // [380]
	"przm_version_1", // [381]
	"frequency", // [382]
	"leftrightarrow", // [383]
	"stratification", // [384]
	"transformations", // [385]
	"cycle", // [386]
	"achieves", // [387]
	"activematcher", // [388]
	"test_przm_dec", // [389]
	"affine", // [390]
	"performance", // [391]
	"product", // [392]
	"sliding", // [393]
	"encoder", // [394]
	"m_len", // [395]
	"index", // [396]
	"programming", // [397]
	"between", // [398]
	"precomputed", // [399]
	"correlation", // [400]
	"equiv", // [401]
	"chunksize", // [402]
	"buffer", // [403]
	"formal", // [404]
	"coefficients", // [405]
	"write_u64_le", // [406]
	"transition", // [407]
	"usize", // [408]
	"following", // [409]
	"dependencies", // [410]
	"stratified", // [411]
	"random", // [412]
	"finite", // [413]
	"branch", // [414]
	"bufkit", // [415]
	"orchestrator", // [416]
	"internal", // [417]
	"matrices", // [418]
	"complete", // [419]
	"variable", // [420]
	"literals", // [421]
	"operator", // [422]
	"display", // [423]
	"rawdict", // [424]
	"cdot", // [425]
	"include", // [426]
	"case", // [427]
	"bridge", // [428]
	"rules", // [429]
	"bits_remaining", // [430]
	"approx", // [431]
	"associative", // [432]
	"integral", // [433]
	"microarchitectural", // [434]
	"relative", // [435]
	"decoder", // [436]
	"implementations", // [437]
	"computational", // [438]
	"dpcandidate", // [439]
	"t_len", // [440]
	"runtime", // [441]
	"driver", // [442]
	"srcidx", // [443]
	"through", // [444]
	"decompress", // [445]
	"chunkcount", // [446]
	"hyperbolic", // [447]
	"dictionaries", // [448]
	"applications", // [449]
	"offsetstreamsize", // [450]
	"break", // [451]
	"systems", // [452]
	"cmd_end", // [453]
	"ratio", // [454]
	"contains", // [455]
	"document", // [456]
	"spectral", // [457]
	"baseline", // [458]
	"margin", // [459]
	"epsilon_i", // [460]
	"fixed", // [461]
	"functions", // [462]
	"source", // [463]
	"structure", // [464]
	"while", // [465]
	"status", // [466]
	"rep1", // [467]
	"using", // [468]
	"block_log2", // [469]
	"completely", // [470]
	"flushliteralsprzm", // [471]
	"uleb128", // [472]
	"font", // [473]
	"serialized", // [474]
	"destination", // [475]
	"blocks", // [476]
	"within", // [477]
	"critical", // [478]
	"gpucompressor", // [479]
	"phase", // [480]
	"updatebracket", // [481]
	"cascaded", // [482]
	"total", // [483]
	"manifold", // [484]
	"blocksizeshift", // [485]
	"simd", // [486]
	"dictlen", // [487]
	"outbuflen", // [488]
	"total_u_size", // [489]
	"chunks", // [490]
	"deficit", // [491]
	"under", // [492]
	"pattern", // [493]
	"isoclinic", // [494]
	"multiplications", // [495]
	"orig_varint_len", // [496]
	"buffers", // [497]
	"leftarrow", // [498]
	"maximum", // [499]
	"cache", // [500]
	"dots", // [501]
	"write_u32_le", // [502]
	"archive", // [503]
	"threads", // [504]
	"that", // [505]
	"dist", // [506]
	"compressor", // [507]
	"benchmarks", // [508]
	"invariants", // [509]
	"homology", // [510]
	"reversible", // [511]
}

var webTokensRawBytes [][]byte
var rfcTopWordsBytes [][]byte

func initRFCTables() {
	webTokensRawBytes = make([][]byte, len(webTokensRaw))
	for i, s := range webTokensRaw {
		webTokensRawBytes[i] = []byte(s)
	}
	rfcTopWordsBytes = make([][]byte, len(rfcTopWords))
	for i, s := range rfcTopWords {
		rfcTopWordsBytes[i] = []byte(s)
	}
}

// applyRFC7932Transform applies one of the 121 RFC 7932 transformations to word,
// writing directly into dst. Strictly 0 dynamic memory allocations.
func applyRFC7932Transform(dst []byte, transID uint8, word []byte) (int, error) {
	if transID >= 121 {
		return 0, fmt.Errorf("%w: invalid RFC transform ID %d", ErrCorruptedFrame, transID)
	}
	pfxIdx := kRFC7932Transforms[transID*3]
	op := kRFC7932Transforms[transID*3+1]
	sfxIdx := kRFC7932Transforms[transID*3+2]

	pfxOff := kRFC7932PrefixSuffixMap[pfxIdx]
	pfxLen := int(kRFC7932PrefixSuffix[pfxOff])
	pfxBytes := kRFC7932PrefixSuffix[pfxOff+1 : pfxOff+1+uint16(pfxLen)]

	sfxOff := kRFC7932PrefixSuffixMap[sfxIdx]
	sfxLen := int(kRFC7932PrefixSuffix[sfxOff])
	sfxBytes := kRFC7932PrefixSuffix[sfxOff+1 : sfxOff+1+uint16(sfxLen)]

	wordLen := len(word)
	midStart := 0
	midLen := wordLen

	if op >= 1 && op <= 9 {
		omit := int(op)
		if wordLen > omit {
			midLen = wordLen - omit
		} else {
			midLen = 0
		}
	} else if op >= 12 && op <= 20 {
		omit := int(op - 11)
		if wordLen > omit {
			midStart = omit
			midLen = wordLen - omit
		} else {
			midLen = 0
		}
	}

	totalLen := pfxLen + midLen + sfxLen
	if totalLen > len(dst) {
		return 0, fmt.Errorf("%w: destination buffer overflow during RFC transform", ErrCorruptedFrame)
	}

	outLen := 0
	if pfxLen > 0 {
		copy(dst[outLen:], pfxBytes)
		outLen += pfxLen
	}

	if midLen > 0 {
		midDst := dst[outLen : outLen+midLen]
		copy(midDst, word[midStart:midStart+midLen])
		if op == 10 { // UppercaseFirst
			if midDst[0] >= 'a' && midDst[0] <= 'z' {
				midDst[0] -= 32
			}
		} else if op == 11 { // UppercaseAll
			for i := 0; i < midLen; i++ {
				c := midDst[i]
				if c >= 'a' && c <= 'z' {
					midDst[i] = c - 32
				}
			}
		}
		outLen += midLen
	}

	if sfxLen > 0 {
		copy(dst[outLen:], sfxBytes)
		outLen += sfxLen
	}

	return outLen, nil
}
