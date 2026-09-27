package snmp

import (
	"slices"
	"strconv"
)

// EnumString formats an SMI enumeration value v. If v is in values, it returns
// the corresponding name in names. If v is not declared, it formats an unknown
// string as typeName(v).
func EnumString(v int32, typeName string, values []int32, names []string) string {
	i, found := slices.BinarySearch(values, v)
	if found && i < len(names) {
		return names[i]
	}
	return typeName + "(" + strconv.FormatInt(int64(v), 10) + ")"
}
