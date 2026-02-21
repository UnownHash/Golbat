package decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

func UpdateIncidentLineup(ctx context.Context, db db.DbDetails, protoReq *pogo.OpenInvasionCombatSessionProto, protoRes *pogo.OpenInvasionCombatSessionOutProto) string {
	incident, unlock, err := getOrCreateIncidentRecord(ctx, db, protoReq.IncidentLookup.IncidentId, protoReq.IncidentLookup.FortId)
	if err != nil {
		return fmt.Sprintf("getOrCreateIncidentRecord: %s", err)
	}
	defer unlock()

	if incident.newRecord {
		log.Debugf("Updating lineup before it was saved: %s", protoReq.IncidentLookup.IncidentId)
	}
	incident.updateFromOpenInvasionCombatSessionOut(protoRes)

	saveIncidentRecord(ctx, db, incident)
	return ""
}

func ConfirmIncident(ctx context.Context, db db.DbDetails, proto *pogo.StartIncidentOutProto) string {
	incident, unlock, err := getOrCreateIncidentRecord(ctx, db, proto.Incident.IncidentId, proto.Incident.FortId)
	if err != nil {
		return fmt.Sprintf("getOrCreateIncidentRecord: %s", err)
	}
	defer unlock()

	if incident.newRecord {
		log.Debugf("Confirming incident before it was saved: %s", proto.Incident.IncidentId)
	}
	incident.updateFromStartIncidentOut(proto)

	saveIncidentRecord(ctx, db, incident)
	return ""
}
