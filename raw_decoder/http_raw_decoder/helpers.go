package http_raw_decoder

import (
	"golbat/pogo"

	log "github.com/sirupsen/logrus"
)

func getValueFromMap(data map[string]interface{}, key1, key2 string) interface{} {
	if v := data[key1]; v != nil {
		return v
	}
	if v := data[key2]; v != nil {
		return v
	}
	return nil
}

func questsHeldHasARTask(quests_held any) *bool {
	const ar_quest_id = int64(pogo.QuestType_QUEST_GEOTARGETED_AR_SCAN)

	quests_held_list, ok := quests_held.([]any)
	if !ok {
		log.Errorf("Raw: unexpected quests_held type in data: %T", quests_held)
		return nil
	}
	for _, quest_id := range quests_held_list {
		if quest_id_f, ok := quest_id.(float64); ok {
			if int64(quest_id_f) == ar_quest_id {
				res := true
				return &res
			}
			continue
		}
		// quest_id is not float64? Treat the whole thing as unknown.
		log.Errorf("Raw: unexpected quest_id type in quests_held: %T", quest_id)
		return nil
	}
	res := false
	return &res
}
