package lex

import "strconv"

// Keyword is one of SMI's reserved words. A reserved word has exactly
// one spelling and cannot be used as a descriptor, so the lexer resolves
// it once and every later pass compares an integer rather than a string.
//
// The set is fixed and shared by every file the process lexes, which is
// what lets a token name a reserved word without carrying a string.
// Identifiers, which vary per file, go through the interner instead.
type Keyword uint8

// The reserved words. RFC 2578 §3.7 is the core of the list; the SMIv1
// trap words from RFC 1215 and the conformance and capabilities words
// from RFC 2580 are here too, because a lexer that does not know them
// reads much of the corpus as ordinary type references.
const (
	KeywordNone Keyword = iota
	KeywordAbsent
	KeywordAccess
	KeywordAgentCapabilities
	KeywordAny
	KeywordApplication
	KeywordAugments
	KeywordBegin
	KeywordBit
	KeywordBits
	KeywordBoolean
	KeywordBy
	KeywordChoice
	KeywordComponent
	KeywordComponents
	KeywordContactInfo
	KeywordCounter32
	KeywordCounter64
	KeywordCreationRequires
	KeywordDefault
	KeywordDefined
	KeywordDefinitions
	KeywordDefval
	KeywordDescription
	KeywordDisplayHint
	KeywordEnd
	KeywordEnterprise
	KeywordEnumerated
	KeywordExplicit
	KeywordExports
	KeywordExternal
	KeywordFalse
	KeywordFrom
	KeywordGauge32
	KeywordGroup
	KeywordIdentifier
	KeywordImplicit
	KeywordImplied
	KeywordImports
	KeywordIncludes
	KeywordIndex
	KeywordInteger
	KeywordInteger32
	KeywordIPAddress
	KeywordLastUpdated
	KeywordMandatoryGroups
	KeywordMax
	KeywordMaxAccess
	KeywordMin
	KeywordMinAccess
	KeywordMinusInfinity
	KeywordModule
	KeywordModuleCompliance
	KeywordModuleIdentity
	KeywordNotificationGroup
	KeywordNotificationType
	KeywordNotifications
	KeywordNull
	KeywordObject
	KeywordObjectGroup
	KeywordObjectIdentity
	KeywordObjectType
	KeywordObjects
	KeywordOctet
	KeywordOf
	KeywordOpaque
	KeywordOptional
	KeywordOrganization
	KeywordPlusInfinity
	KeywordPresent
	KeywordPrivate
	KeywordProductRelease
	KeywordReal
	KeywordReference
	KeywordRevision
	KeywordSequence
	KeywordSet
	KeywordSize
	KeywordStatus
	KeywordString
	KeywordSupports
	KeywordSyntax
	KeywordTags
	KeywordTextualConvention
	KeywordTimeTicks
	KeywordTrapType
	KeywordTrue
	KeywordUnits
	KeywordUniversal
	KeywordUnsigned32
	KeywordVariables
	KeywordVariation
	KeywordWith
	KeywordWriteSyntax
)

// keywordText is each reserved word's spelling, indexed by its constant
// rather than written in declaration order, so reordering the block
// above cannot silently re-point a word at the wrong constant.
//
// Spelling is case-sensitive: SMI writes Counter32 and INTEGER and means
// two different things by the difference.
var keywordText = [...]string{
	KeywordNone:              "",
	KeywordAbsent:            "ABSENT",
	KeywordAccess:            "ACCESS",
	KeywordAgentCapabilities: "AGENT-CAPABILITIES",
	KeywordAny:               "ANY",
	KeywordApplication:       "APPLICATION",
	KeywordAugments:          "AUGMENTS",
	KeywordBegin:             "BEGIN",
	KeywordBit:               "BIT",
	KeywordBits:              "BITS",
	KeywordBoolean:           "BOOLEAN",
	KeywordBy:                "BY",
	KeywordChoice:            "CHOICE",
	KeywordComponent:         "COMPONENT",
	KeywordComponents:        "COMPONENTS",
	KeywordContactInfo:       "CONTACT-INFO",
	KeywordCounter32:         "Counter32",
	KeywordCounter64:         "Counter64",
	KeywordCreationRequires:  "CREATION-REQUIRES",
	KeywordDefault:           "DEFAULT",
	KeywordDefined:           "DEFINED",
	KeywordDefinitions:       "DEFINITIONS",
	KeywordDefval:            "DEFVAL",
	KeywordDescription:       "DESCRIPTION",
	KeywordDisplayHint:       "DISPLAY-HINT",
	KeywordEnd:               "END",
	KeywordEnterprise:        "ENTERPRISE",
	KeywordEnumerated:        "ENUMERATED",
	KeywordExplicit:          "EXPLICIT",
	KeywordExports:           "EXPORTS",
	KeywordExternal:          "EXTERNAL",
	KeywordFalse:             "FALSE",
	KeywordFrom:              "FROM",
	KeywordGauge32:           "Gauge32",
	KeywordGroup:             "GROUP",
	KeywordIdentifier:        "IDENTIFIER",
	KeywordImplicit:          "IMPLICIT",
	KeywordImplied:           "IMPLIED",
	KeywordImports:           "IMPORTS",
	KeywordIncludes:          "INCLUDES",
	KeywordIndex:             "INDEX",
	KeywordInteger:           "INTEGER",
	KeywordInteger32:         "Integer32",
	KeywordIPAddress:         "IpAddress",
	KeywordLastUpdated:       "LAST-UPDATED",
	KeywordMandatoryGroups:   "MANDATORY-GROUPS",
	KeywordMax:               "MAX",
	KeywordMaxAccess:         "MAX-ACCESS",
	KeywordMin:               "MIN",
	KeywordMinAccess:         "MIN-ACCESS",
	KeywordMinusInfinity:     "MINUS-INFINITY",
	KeywordModule:            "MODULE",
	KeywordModuleCompliance:  "MODULE-COMPLIANCE",
	KeywordModuleIdentity:    "MODULE-IDENTITY",
	KeywordNotificationGroup: "NOTIFICATION-GROUP",
	KeywordNotificationType:  "NOTIFICATION-TYPE",
	KeywordNotifications:     "NOTIFICATIONS",
	KeywordNull:              "NULL",
	KeywordObject:            "OBJECT",
	KeywordObjectGroup:       "OBJECT-GROUP",
	KeywordObjectIdentity:    "OBJECT-IDENTITY",
	KeywordObjectType:        "OBJECT-TYPE",
	KeywordObjects:           "OBJECTS",
	KeywordOctet:             "OCTET",
	KeywordOf:                "OF",
	KeywordOpaque:            "Opaque",
	KeywordOptional:          "OPTIONAL",
	KeywordOrganization:      "ORGANIZATION",
	KeywordPlusInfinity:      "PLUS-INFINITY",
	KeywordPresent:           "PRESENT",
	KeywordPrivate:           "PRIVATE",
	KeywordProductRelease:    "PRODUCT-RELEASE",
	KeywordReal:              "REAL",
	KeywordReference:         "REFERENCE",
	KeywordRevision:          "REVISION",
	KeywordSequence:          "SEQUENCE",
	KeywordSet:               "SET",
	KeywordSize:              "SIZE",
	KeywordStatus:            "STATUS",
	KeywordString:            "STRING",
	KeywordSupports:          "SUPPORTS",
	KeywordSyntax:            "SYNTAX",
	KeywordTags:              "TAGS",
	KeywordTextualConvention: "TEXTUAL-CONVENTION",
	KeywordTimeTicks:         "TimeTicks",
	KeywordTrapType:          "TRAP-TYPE",
	KeywordTrue:              "TRUE",
	KeywordUnits:             "UNITS",
	KeywordUniversal:         "UNIVERSAL",
	KeywordUnsigned32:        "Unsigned32",
	KeywordVariables:         "VARIABLES",
	KeywordVariation:         "VARIATION",
	KeywordWith:              "WITH",
	KeywordWriteSyntax:       "WRITE-SYNTAX",
}

// String returns the reserved word's exact spelling. The string comes
// from the fixed table, so a caller may hold onto it without pinning the
// source buffer the word was read from. An off-table value renders as
// "keyword(N)" rather than panicking, because rendering a token must
// never be the thing that fails.
func (k Keyword) String() string {
	if int(k) >= len(keywordText) {
		return "keyword(" + strconv.Itoa(int(k)) + ")"
	}

	return keywordText[k]
}

// keywords resolves a spelling to its reserved word. It is built once at
// init and only read afterwards, which is what makes it safe to share
// across goroutines lexing different files.
var keywords = func() map[string]Keyword {
	m := make(map[string]Keyword, len(keywordText))
	for k, text := range keywordText {
		if text != "" {
			m[text] = Keyword(k)
		}
	}

	return m
}()

// lookupKeyword resolves name against the reserved-word set. Indexing a
// string-keyed map with a byte slice is the one conversion the compiler
// performs without copying, which is why the parameter is bytes.
func lookupKeyword(name []byte) (Keyword, bool) {
	k, ok := keywords[string(name)]

	return k, ok
}
