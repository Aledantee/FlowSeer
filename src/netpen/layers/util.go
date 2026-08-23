// Shared helpers for owned protocol layers.

package layers

import "crypto/md5"

// md5sum returns the MD5 digest of data. The baseline's VTP summary
// advertisement carries an MD5 over zeros(16) + body + vlan_records +
// zeros(16); this helper computes the raw digest the serializer needs.
func md5sum(data []byte) []byte {
	sum := md5.Sum(data)
	return sum[:]
}
