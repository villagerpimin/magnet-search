package aggregate

// 本文件集中存放标题词元的词表：语言、画质、版本形态。
// 词表独立成文件是为了让分词逻辑保持精简，也便于按站点习惯增补。

// langTokens 把语言/字幕标记映射到规范语言码。
var langTokens = map[string]string{
	"en": "en", "eng": "en", "english": "en", "engsub": "en",
	"zh": "zh", "chi": "zh", "chs": "zh", "cht": "zh", "zht": "zh",
	"chinese": "zh", "mandarin": "zh", "cantonese": "zh",
	"ja": "ja", "jpn": "ja", "japanese": "ja", "jpnsub": "ja",
	"ko": "ko", "kor": "ko", "korean": "ko",
	"ru": "ru", "rus": "ru", "russian": "ru",
	"fr": "fr", "fre": "fr", "french": "fr", "truefrench": "fr", "vff": "fr",
	"de": "de", "ger": "de", "german": "de",
	"es": "es", "spa": "es", "spanish": "es", "castellano": "es",
	"pt": "pt", "por": "pt", "portuguese": "pt",
	"it": "it", "ita": "it", "italian": "it",
	"pl": "pl", "pol": "pl", "polish": "pl",
	"tr": "tr", "tur": "tr", "turkish": "tr",
	"ar": "ar", "ara": "ar", "arabic": "ar",
	"hi": "hi", "hin": "hi", "hindi": "hi",
	"th": "th", "tha": "th", "thai": "th",
	"vi": "vi", "vie": "vi", "vietnamese": "vi",
	"id": "id", "ind": "id", "indonesian": "id",
	"nl": "nl", "dut": "nl", "dutch": "nl",
	"sv": "sv", "swe": "sv", "swedish": "sv",
	"no": "no", "nor": "no", "norwegian": "no",
	"da": "da", "dan": "da", "danish": "da",
	"fi": "fi", "fin": "fi", "finnish": "fi",
	"cs": "cs", "cze": "cs", "czech": "cs",
	"hu": "hu", "hun": "hu", "hungarian": "hu",
	"el": "el", "gre": "el", "greek": "el",
	"he": "he", "heb": "he", "hebrew": "he",
	"uk": "uk", "ukr": "uk", "ukrainian": "uk",
	"multi": "multi", "multilingual": "multi", "multiple": "multi", "dual": "multi",
}

// langMarkers 是字幕/版本相关的标记词，属于语言维度但本身不指向具体语言。
var langMarkers = map[string]bool{
	"subtitle": true, "subtitles": true, "subs": true, "sub": true,
	"simplified": true, "traditional": true, "softsubs": true, " hardsub": true,
}

// qualityTokens 是画质、编码、来源、音频相关的标记词。
var qualityTokens = map[string]bool{
	// 分辨率
	"2160p": true, "1080p": true, "1080i": true, "720p": true, "576p": true,
	"480p": true, "360p": true, "4k": true, "8k": true, "uhd": true, "fhd": true,
	"hd": true, "sd": true,
	// 编码
	"x264": true, "x265": true, "h264": true, "h265": true, "hevc": true,
	"avc": true, "xvid": true, "divx": true, "av1": true, "vp9": true,
	"10bit": true, "8bit": true, "hi10p": true,
	// 来源
	"bluray": true, "blu": true, "bdrip": true, "brrip": true, "remux": true,
	"webrip": true, "webdl": true, "web": true, "hdtv": true, "pdtv": true,
	"dvdrip": true, "dvd": true, "hddvd": true, "cam": true, "ts": true,
	"tc": true, "r5": true, "hc": true, "korsub": true,
	// 色彩与音频
	"hdr": true, "hdr10": true, "dv": true, "sdr": true, "atmos": true,
	"truehd": true, "dts": true, "dtshd": true, "ddp": true, "dd": true,
	"aac": true, "flac": true, "lpcm": true, "opus": true, "mp3": true, "m4a": true,
}

// editionTokens 是发行形态相关的标记词。
var editionTokens = map[string]bool{
	"proper": true, "repack": true, "extended": true, "unrated": true,
	"directors": true, "cut": true, "retail": true, "internal": true,
	"limited": true, "remastered": true, "readnfo": true, "hdc": true,
	"bonus": true, "uncensored": true, "censored": true, "omake": true,
	"complete": true, "real": true, "hdcam": true, "imax": true, "vhs": true,
}

// resolutionTiers 把分辨率标记映射到画质档位。
var resolutionTiers = map[string]string{
	"2160p": "uhd", "4k": "uhd", "8k": "uhd", "uhd": "uhd",
	"1080p": "1080p", "1080i": "1080p", "fhd": "1080p",
	"720p": "720p",
	"576p": "sd", "480p": "sd", "360p": "sd", "sd": "sd",
}
