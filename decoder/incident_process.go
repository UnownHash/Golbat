package decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

func UpdateIncidentLineup(ctx context.Context, db db.DbDetails, protoReq *pogo.OpenInvasionCombatSessionProto, protoRes *pogo.OpenInvasionCombatSessionOutProto) string {
	fortId, ok := ParseFortId(protoReq.IncidentLookup.FortId)
	if !ok {
		log.Errorf("UpdateIncidentLineup: unparseable fort id %q", protoReq.IncidentLookup.FortId)
		return fmt.Sprintf("unparseable fort id %q", protoReq.IncidentLookup.FortId)
	}
	incident, unlock, err := getOrCreateIncidentRecord(ctx, db, protoReq.IncidentLookup.IncidentId, fortId, "UpdateIncidentWithConfirmation")
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

func UpdateIncidentLineupFromBattleState(ctx context.Context, db db.DbDetails, fortId, incidentId string, out *pogo.BattleStateOutProto) string {
	parsedFortId, ok := ParseFortId(fortId)
	if !ok {
		log.Errorf("UpdateIncidentLineupFromBattleState: unparseable fort id %q", fortId)
		return fmt.Sprintf("unparseable fort id %q", fortId)
	}
	incident, unlock, err := getOrCreateIncidentRecord(ctx, db, incidentId, parsedFortId, "UpdateIncidentLineupFromBattleState")
	if err != nil {
		return fmt.Sprintf("getOrCreateIncidentRecord: %s", err)
	}
	defer unlock()

	incident.updateFromBattleState(out)
	saveIncidentRecord(ctx, db, incident)
	return ""
}

func ConfirmIncident(ctx context.Context, db db.DbDetails, proto *pogo.StartIncidentOutProto) string {
	fortId, ok := ParseFortId(proto.Incident.FortId)
	if !ok {
		log.Errorf("ConfirmIncident: unparseable fort id %q", proto.Incident.FortId)
		return fmt.Sprintf("unparseable fort id %q", proto.Incident.FortId)
	}
	incident, unlock, err := getOrCreateIncidentRecord(ctx, db, proto.Incident.IncidentId, fortId, "UpdateIncidentFromInvasion")
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
