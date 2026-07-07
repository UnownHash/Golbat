package decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogoshim"
)

func UpdateIncidentLineup(ctx context.Context, db db.DbDetails, protoReq pogoshim.OpenInvasionCombatSessionProto, protoRes pogoshim.OpenInvasionCombatSessionOutProto) string {
	incidentLookup := protoReq.GetIncidentLookup()
	incident, unlock, err := getOrCreateIncidentRecord(ctx, db, incidentLookup.GetIncidentId(), incidentLookup.GetFortId(), "UpdateIncidentWithConfirmation")
	if err != nil {
		return fmt.Sprintf("getOrCreateIncidentRecord: %s", err)
	}
	defer unlock()

	if incident.newRecord {
		log.Debugf("Updating lineup before it was saved: %s", incidentLookup.GetIncidentId())
	}
	incident.updateFromOpenInvasionCombatSessionOut(protoRes)

	saveIncidentRecord(ctx, db, incident)
	return ""
}

func UpdateIncidentLineupFromBattleState(ctx context.Context, db db.DbDetails, fortId, incidentId string, out pogoshim.BattleStateOutProto) string {
	incident, unlock, err := getOrCreateIncidentRecord(ctx, db, incidentId, fortId, "UpdateIncidentLineupFromBattleState")
	if err != nil {
		return fmt.Sprintf("getOrCreateIncidentRecord: %s", err)
	}
	defer unlock()

	incident.updateFromBattleState(out)
	saveIncidentRecord(ctx, db, incident)
	return ""
}

func ConfirmIncident(ctx context.Context, db db.DbDetails, proto pogoshim.StartIncidentOutProto) string {
	incidentInfo := proto.GetIncident()
	incident, unlock, err := getOrCreateIncidentRecord(ctx, db, incidentInfo.GetIncidentId(), incidentInfo.GetFortId(), "UpdateIncidentFromInvasion")
	if err != nil {
		return fmt.Sprintf("getOrCreateIncidentRecord: %s", err)
	}
	defer unlock()

	if incident.newRecord {
		log.Debugf("Confirming incident before it was saved: %s", incidentInfo.GetIncidentId())
	}
	incident.updateFromStartIncidentOut(proto)

	saveIncidentRecord(ctx, db, incident)
	return ""
}
