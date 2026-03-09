//go:build dbdebug

package decoder

import (
	"strings"

	log "github.com/sirupsen/logrus"
)

// dbDebugEnabled is true when built with -tags dbdebug
const dbDebugEnabled = true

// dbDebugLog logs a database operation with changed fields
func dbDebugLog(reason, entityType, id string, changedFields []string) {
	fields := ""
	if len(changedFields) > 0 {
		fields = " changed=[" + strings.Join(changedFields, ", ") + "]"
	}
	log.Debugf("[DB_%s] %s id=%s%s", reason, entityType, id, fields)
}
