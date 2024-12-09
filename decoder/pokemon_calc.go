package decoder

import (
	"context"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"golbat/db"
	"golbat/grpc"
	"golbat/pogo"
	prot "google.golang.org/protobuf/proto"
	"gopkg.in/guregu/null.v4"
	"strconv"
)

// / pokemonCalc struct for performing internal calculations per transaction with some caching support
type pokemonCalc struct {
	pokemon *Pokemon
	ctx     context.Context
	db      db.DbDetails
	weather map[int64]pogo.GameplayWeatherProto_WeatherCondition
}

func (c *pokemonCalc) populateInternal() {
	if len(c.pokemon.GolbatInternal) == 0 || len(c.pokemon.internal.ScanHistory) != 0 {
		return
	}
	err := prot.Unmarshal(c.pokemon.GolbatInternal, &c.pokemon.internal)
	if err != nil {
		log.Warnf("Failed to parse internal data for %s: %s", c.pokemon.Id, err)
		c.pokemon.internal.Reset()
	}
}

func (c *pokemonCalc) locateScan(isStrong bool, isBoosted bool) (*grpc.PokemonScan, bool) {
	c.populateInternal()
	var bestMatching *grpc.PokemonScan
	for _, entry := range c.pokemon.internal.ScanHistory {
		if entry.Strong != isStrong {
			continue
		}
		if entry.Weather != int32(pogo.GameplayWeatherProto_NONE) == isBoosted {
			return entry, true
		} else {
			bestMatching = entry
		}
	}
	return bestMatching, false
}

func (c *pokemonCalc) locateAllScans() (unboosted, boosted, strong *grpc.PokemonScan) {
	c.populateInternal()
	for _, entry := range c.pokemon.internal.ScanHistory {
		if entry.Strong {
			strong = entry
		} else if entry.Weather != int32(pogo.GameplayWeatherProto_NONE) {
			boosted = entry
		} else {
			unboosted = entry
		}
	}
	return
}

func (c *pokemonCalc) setPokemonDisplay(pokemonId int16, display *pogo.PokemonDisplayProto) {
	if !c.pokemon.isNewRecord() {
		// If we would like to support detect A/B spawn in the future, fill in more code here from Chuck
		var oldId int16
		if c.pokemon.IsDitto {
			oldId = int16(c.pokemon.DisplayPokemonId.ValueOrZero())
		} else {
			oldId = c.pokemon.PokemonId
		}
		if oldId != pokemonId || c.pokemon.Form != null.IntFrom(int64(display.Form)) ||
			c.pokemon.Costume != null.IntFrom(int64(display.Costume)) ||
			c.pokemon.Gender != null.IntFrom(int64(display.Gender)) ||
			c.pokemon.IsStrong.ValueOrZero() != display.IsStrongPokemon {
			log.Debugf("Pokemon %s changed from (%d,%d,%d,%d,%t) to (%d,%d,%d,%d,%t)", c.pokemon.Id, oldId,
				c.pokemon.Form.ValueOrZero(), c.pokemon.Costume.ValueOrZero(), c.pokemon.Gender.ValueOrZero(),
				c.pokemon.IsStrong.ValueOrZero(),
				pokemonId, display.Form, display.Costume, display.Gender, display.IsStrongPokemon)
			c.pokemon.Weight = null.NewFloat(0, false)
			c.pokemon.Height = null.NewFloat(0, false)
			c.pokemon.Size = null.NewInt(0, false)
			c.pokemon.Move1 = null.NewInt(0, false)
			c.pokemon.Move2 = null.NewInt(0, false)
			c.pokemon.Cp = null.NewInt(0, false)
			c.pokemon.Shiny = null.NewBool(false, false)
			c.pokemon.IsDitto = false
			c.pokemon.DisplayPokemonId = null.NewInt(0, false)
			c.pokemon.Pvp = null.NewString("", false)
		}
	}
	if c.pokemon.isNewRecord() || !c.pokemon.IsDitto {
		c.pokemon.PokemonId = pokemonId
	}
	c.pokemon.Gender = null.IntFrom(int64(display.Gender))
	c.pokemon.Form = null.IntFrom(int64(display.Form))
	c.pokemon.Costume = null.IntFrom(int64(display.Costume))
	if !c.pokemon.isNewRecord() {
		c.repopulateIv(int64(display.WeatherBoostedCondition), display.IsStrongPokemon)
	}
	c.pokemon.Weather = null.IntFrom(int64(display.WeatherBoostedCondition))
	c.pokemon.IsStrong = null.BoolFrom(display.IsStrongPokemon)
}

func (c *pokemonCalc) clearIv(cp bool) {
	c.pokemon.AtkIv = null.NewInt(0, false)
	c.pokemon.DefIv = null.NewInt(0, false)
	c.pokemon.StaIv = null.NewInt(0, false)
	c.pokemon.Iv = null.NewFloat(0, false)
	if cp {
		switch c.pokemon.SeenType.ValueOrZero() {
		case SeenType_LureEncounter:
			c.pokemon.SeenType = null.StringFrom(SeenType_LureWild)
		case SeenType_Encounter:
			c.pokemon.SeenType = null.StringFrom(SeenType_Wild)
		}
		c.pokemon.Cp = null.NewInt(0, false)
		c.pokemon.Pvp = null.NewString("", false)
	}
}

func (c *pokemonCalc) calculateIv(a int64, d int64, s int64) {
	c.pokemon.AtkIv = null.IntFrom(a)
	c.pokemon.DefIv = null.IntFrom(d)
	c.pokemon.StaIv = null.IntFrom(s)
	c.pokemon.Iv = null.FloatFrom(float64(a+d+s) / .45)
}

func (c *pokemonCalc) repopulateIv(weather int64, isStrong bool) {
	var isBoosted bool
	if !c.pokemon.IsDitto {
		isBoosted = weather != int64(pogo.GameplayWeatherProto_NONE)
		if isStrong == c.pokemon.IsStrong.ValueOrZero() &&
			c.pokemon.Weather.ValueOrZero() != int64(pogo.GameplayWeatherProto_NONE) == isBoosted {
			return
		}
	} else if isStrong {
		log.Errorf("Strong Ditto??? I can't handle this fml %s", c.pokemon.Id)
		c.clearIv(true)
		return
	} else {
		isBoosted = weather == int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
		// both Ditto and disguise are boosted and Ditto was not boosted: none -> boosted
		// or both Ditto and disguise were boosted and Ditto is not boosted: boosted -> none
		if c.pokemon.Weather.ValueOrZero() == int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY) == isBoosted {
			return
		}
	}
	matchingScan, isBoostedMatches := c.locateScan(isStrong, isBoosted)
	var oldAtk, oldDef, oldSta int64
	if matchingScan == nil {
		c.pokemon.Level = null.NewInt(0, false)
		c.clearIv(true)
	} else {
		oldLevel := c.pokemon.Level.ValueOrZero()
		if c.pokemon.AtkIv.Valid {
			oldAtk = c.pokemon.AtkIv.Int64
			oldDef = c.pokemon.DefIv.Int64
			oldSta = c.pokemon.StaIv.Int64
		} else {
			oldAtk = -1
			oldDef = -1
			oldSta = -1
		}
		c.pokemon.Level = null.IntFrom(int64(matchingScan.Level))
		if isBoostedMatches || isStrong { // strong Pokemon IV is unaffected by weather
			c.calculateIv(int64(matchingScan.Attack), int64(matchingScan.Defense), int64(matchingScan.Stamina))
			switch c.pokemon.SeenType.ValueOrZero() {
			case SeenType_LureWild:
				c.pokemon.SeenType = null.StringFrom(SeenType_LureEncounter)
			case SeenType_Wild:
				c.pokemon.SeenType = null.StringFrom(SeenType_Encounter)
			}
		} else {
			c.clearIv(true)
		}
		if !isBoostedMatches {
			if isBoosted {
				c.pokemon.Level.Int64 += 5
			} else {
				c.pokemon.Level.Int64 -= 5
			}
		}
		if c.pokemon.Level.Int64 != oldLevel || c.pokemon.AtkIv.Valid &&
			(c.pokemon.AtkIv.Int64 != oldAtk || c.pokemon.DefIv.Int64 != oldDef || c.pokemon.StaIv.Int64 != oldSta) {
			c.pokemon.Cp = null.NewInt(0, false)
			c.pokemon.Pvp = null.NewString("", false)
		}
	}
}

func (c *pokemonCalc) addWildPokemon(wildPokemon *pogo.WildPokemonProto, timestampMs int64) {
	if strconv.FormatUint(wildPokemon.EncounterId, 10) != c.pokemon.Id {
		panic("Unmatched EncounterId")
	}
	c.pokemon.Lat = wildPokemon.Latitude
	c.pokemon.Lon = wildPokemon.Longitude

	c.pokemon.updateSpawnpointInfo(c.ctx, c.db, wildPokemon, timestampMs)
	c.setPokemonDisplay(int16(wildPokemon.Pokemon.PokemonId), wildPokemon.Pokemon.PokemonDisplay)
}

func checkScans(old *grpc.PokemonScan, new *grpc.PokemonScan) error {
	if old == nil || old.CompressedIv() == new.CompressedIv() {
		return nil
	}
	return errors.New(fmt.Sprintf("Unexpected IV mismatch %s != %s", old, new))
}

func (c *pokemonCalc) setDittoAttributes(mode string, isDitto bool, old, new *grpc.PokemonScan) {
	if isDitto {
		log.Debugf("[POKEMON] %s: %s Ditto found %s -> %s", c.pokemon.Id, mode, old, new)
		c.pokemon.IsDitto = true
		c.pokemon.DisplayPokemonId = null.IntFrom(int64(c.pokemon.PokemonId))
		c.pokemon.PokemonId = int16(pogo.HoloPokemonId_DITTO)
	} else {
		log.Debugf("[POKEMON] %s: %s not Ditto found %s -> %s", c.pokemon.Id, mode, old, new)
	}
}
func (c *pokemonCalc) resetDittoAttributes(mode string, old, aux, new *grpc.PokemonScan) (*grpc.PokemonScan, error) {
	log.Debugf("[POKEMON] %s: %s Ditto was reset %s (%s) -> %s", c.pokemon.Id, mode, old, aux, new)
	c.pokemon.IsDitto = false
	c.pokemon.DisplayPokemonId = null.NewInt(0, false)
	c.pokemon.PokemonId = int16(c.pokemon.DisplayPokemonId.Int64)
	return new, checkScans(old, new)
}

// detectDitto returns the IV/level set that should be used for persisting to db/seen if caught.
// error is set if something unexpected happened and the scan history should be cleared.
func (c *pokemonCalc) detectDitto(scan *grpc.PokemonScan) (*grpc.PokemonScan, error) {
	unboostedScan, boostedScan, strongScan := c.locateAllScans()
	if scan.Strong {
		if strongScan != nil {
			expectedLevel := strongScan.Level
			isBoosted := scan.Weather != int32(pogo.GameplayWeatherProto_NONE)
			if strongScan.Weather != int32(pogo.GameplayWeatherProto_NONE) != isBoosted {
				if isBoosted {
					expectedLevel += 5
				} else {
					expectedLevel -= 5
				}
			}
			if scan.Level != expectedLevel || scan.CompressedIv() != strongScan.CompressedIv() {
				return scan, errors.New(fmt.Sprintf("Unexpected strong Pokemon (Ditto?), %s -> %s",
					strongScan, scan))
			}
		}
		return scan, nil
	}

	// Here comes the Ditto logic. Embrace yourself :)
	// Ditto weather can be split into 4 categories:
	//   - 00: No weather boost
	//   - 0P: No weather boost but Ditto is actually boosted by partly cloudy causing seen IV to be boosted [atypical]
	//   - B0: Weather boosts disguise but not Ditto causing seen IV to be unboosted [atypical]
	//   - PP: Weather being partly cloudy boosts both disguise and Ditto
	//
	// We will also use 0N/BN/PN to denote a normal non-Ditto spawn with corresponding weather boosts.
	// Disguise IV depends on Ditto weather boost instead, and caught Ditto is boosted only in PP state.
	if c.pokemon.IsDitto {
		// If IsDitto = true, then the IV sets in history are ALWAYS confirmed
		scan.Confirmed = true
		var unboostedLevel int32
		if boostedScan == nil {
			unboostedLevel = unboostedScan.Level
		} else {
			unboostedLevel = boostedScan.Level - 5
		}
		switch scan.Weather {
		case int32(pogo.GameplayWeatherProto_NONE):
			if scan.CellWeather == int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
				switch scan.Level {
				case unboostedLevel:
					return c.resetDittoAttributes("0N", unboostedScan, boostedScan, scan)
				case unboostedLevel + 5:
					// For a confirmed Ditto, we persist IV in inactive only in 0P state
					// when disguise is boosted, it has same IV as Ditto
					scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
					return unboostedScan, checkScans(boostedScan, scan)
				}
				return scan, errors.New(fmt.Sprintf("Unexpected 0P Ditto level change, %s/%s -> %s",
					unboostedScan, boostedScan, scan))
			}
			return scan, checkScans(unboostedScan, scan)
		case int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
			return scan, checkScans(boostedScan, scan)
		}
		switch scan.Level {
		case unboostedLevel:
			scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			return scan, checkScans(unboostedScan, scan)
		case unboostedLevel + 5:
			return c.resetDittoAttributes("BN", boostedScan, unboostedScan, scan)
		}
		return scan, errors.New(fmt.Sprintf("Unexpected B0 Ditto level change, %s/%s -> %s",
			unboostedScan, boostedScan, scan))
	}

	isBoosted := scan.Weather != int32(pogo.GameplayWeatherProto_NONE)
	var matchingScan *grpc.PokemonScan
	if unboostedScan != nil || boostedScan != nil {
		if unboostedScan != nil && boostedScan != nil { // if we have both IVs then they must be correct
			if unboostedScan.Level == scan.Level {
				if isBoosted {
					c.setDittoAttributes(">B0", true, unboostedScan, scan)
					scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
					return scan, nil
				}
				return scan, checkScans(unboostedScan, scan)
			} else if boostedScan.Level == scan.Level {
				if isBoosted {
					return scan, checkScans(boostedScan, scan)
				}
				c.setDittoAttributes(">0P", true, boostedScan, scan)
				scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
				return unboostedScan, nil
			}
			return scan, errors.New(fmt.Sprintf("Unexpected third level found %s, %s vs %s",
				unboostedScan, boostedScan, scan))
		}

		levelAdjustment := int32(0)
		if isBoosted {
			if boostedScan != nil {
				matchingScan = boostedScan
			} else {
				matchingScan = unboostedScan
				levelAdjustment = 5
			}
		} else {
			if unboostedScan != nil {
				matchingScan = unboostedScan
			} else {
				matchingScan = boostedScan
				levelAdjustment = -5
			}
		}
		switch scan.Level - (matchingScan.Level + levelAdjustment) {
		case 0:
		// the Pokémon has been encountered before, but we find an unexpected level when reencountering it => Ditto
		// note that at this point the level should have been already readjusted according to the new weather boost
		case 5:
			switch scan.Weather {
			case int32(pogo.GameplayWeatherProto_NONE):
				switch matchingScan.Weather {
				case int32(pogo.GameplayWeatherProto_NONE):
					c.setDittoAttributes("00/0N>0P", true, matchingScan, scan)
					scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
					return unboostedScan, nil
				case int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
					if err := checkScans(matchingScan, scan); err != nil {
						return scan, err
					}
					c.setDittoAttributes("PN>0P", true, matchingScan, scan)
					scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
					scan.Confirmed = true
					return unboostedScan, nil
				}
				if err := checkScans(matchingScan, scan); err != nil {
					return scan, err
				}
				if scan.CellWeather != int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
					if scan.MustHaveRerolled(matchingScan) {
						c.setDittoAttributes("B0>00/[0N]", false, matchingScan, scan)
					} else {
						// set Ditto as it is most likely B0>00 if species did not reroll
						c.setDittoAttributes("B0>[00]/0N", true, matchingScan, scan)
					}
					scan.Confirmed = true
				} else if matchingScan.Confirmed || scan.MustBeBoosted() {
					c.setDittoAttributes("BN>0P", true, matchingScan, scan)
					scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
					scan.Confirmed = true
					return unboostedScan, nil
					// scan.MustBeUnboosted() need not be checked since matchingScan would not have been in B0
				} else {
					// in case of BN>0P, we set Ditto to be a hidden 0P state, hoping we rediscover later
					// setting 0P Ditto would also mean that we have a Ditto with unconfirmed IV which is a bad idea
					c.setDittoAttributes("BN>0P or B0>[0N]", false, matchingScan, scan)
				}
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			case int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
				// we can never be sure if this is a Ditto or rerolling into non-Ditto
				if scan.MustHaveRerolled(matchingScan) {
					c.setDittoAttributes("B0>PP/[PN]", false, matchingScan, scan)
				} else {
					c.setDittoAttributes("B0>[PP]/PN", true, matchingScan, scan)
				}
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			default:
				c.setDittoAttributes("B0>BN", false, matchingScan, scan)
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			}
			return scan, nil
		case -5:
			switch scan.Weather {
			case int32(pogo.GameplayWeatherProto_NONE):
				// we can never be sure if this is a Ditto or rerolling into non-Ditto
				if scan.MustHaveRerolled(matchingScan) {
					c.setDittoAttributes("0P>00/[0N]", false, matchingScan, scan)
				} else {
					c.setDittoAttributes("0P>[00]/0N", true, matchingScan, scan)
				}
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
				return scan, nil
			case int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
				c.setDittoAttributes("0P>PN", false, matchingScan, scan)
				matchingScan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
				scan.Confirmed = true
				return scan, checkScans(matchingScan, scan)
			}
			switch matchingScan.Weather {
			case int32(pogo.GameplayWeatherProto_NONE):
				if err := checkScans(matchingScan, scan); err != nil {
					return scan, err
				}
				if scan.MustBeBoosted() {
					c.setDittoAttributes("0P>BN", false, matchingScan, scan)
					matchingScan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
					scan.Confirmed = true
				} else if matchingScan.Confirmed || // this covers scan.MustBeUnboosted()
					matchingScan.CellWeather != int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
					c.setDittoAttributes("00/0N>B0", true, matchingScan, scan)
					scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
					scan.Confirmed = true
				} else {
					// same rationale as BN>0P or B0>[0N]
					c.setDittoAttributes("0N>B0 or 0P>[BN]", false, matchingScan, scan)
					matchingScan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
				}
				return scan, nil
			case int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY):
				c.setDittoAttributes("PP/PN>B0", true, matchingScan, scan)
			default:
				c.setDittoAttributes("BN>B0", true, matchingScan, scan)
			}
			scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			return scan, nil
		case 10:
			c.setDittoAttributes("B0>0P", true, matchingScan, scan)
			matchingScan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
			return matchingScan, nil // unboostedScan is a wrong guess in this case
		case -10:
			c.setDittoAttributes("0P>B0", true, matchingScan, scan)
			matchingScan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
			scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			return scan, nil
		default:
			return scan, errors.New(fmt.Sprintf("Unexpected level %s -> %s", matchingScan, scan))
		}
	}
	if isBoosted {
		if scan.MustBeUnboosted() {
			c.setDittoAttributes("B0", true, matchingScan, scan)
			scan.Weather = int32(pogo.GameplayWeatherProto_NONE)
			scan.Confirmed = true
			return scan, checkScans(unboostedScan, scan)
		}
		scan.Confirmed = scan.MustBeBoosted()
		return scan, checkScans(boostedScan, scan)
	} else if scan.MustBeBoosted() {
		c.setDittoAttributes("0P", true, matchingScan, scan)
		scan.Weather = int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
		scan.Confirmed = true
		return unboostedScan, checkScans(boostedScan, scan)
	}
	scan.Confirmed = scan.MustBeUnboosted()
	return scan, checkScans(unboostedScan, scan)
}

// caller should setPokemonDisplay prior to calling this
func (c *pokemonCalc) addEncounterPokemon(proto *pogo.PokemonProto, username string) {
	c.pokemon.Username = null.StringFrom(username)
	c.pokemon.Shiny = null.BoolFrom(proto.PokemonDisplay.Shiny)
	c.pokemon.Cp = null.IntFrom(int64(proto.Cp))
	c.pokemon.Move1 = null.IntFrom(int64(proto.Move1))
	c.pokemon.Move2 = null.IntFrom(int64(proto.Move2))
	c.pokemon.Height = null.FloatFrom(float64(proto.HeightM))
	c.pokemon.Size = null.IntFrom(int64(proto.Size))
	c.pokemon.Weight = null.FloatFrom(float64(proto.WeightKg))

	scan := grpc.PokemonScan{
		Weather:     int32(c.pokemon.Weather.Int64),
		Strong:      c.pokemon.IsStrong.Bool,
		Attack:      proto.IndividualAttack,
		Defense:     proto.IndividualDefense,
		Stamina:     proto.IndividualStamina,
		CellWeather: int32(c.pokemon.Weather.Int64),
		Pokemon:     int32(proto.PokemonId),
		Costume:     int32(proto.PokemonDisplay.Costume),
		Gender:      int32(proto.PokemonDisplay.Gender),
		Form:        int32(proto.PokemonDisplay.Form),
	}
	if scan.CellWeather == int32(pogo.GameplayWeatherProto_NONE) {
		weather, err := getWeatherRecord(c.ctx, c.db, weatherCellIdFromLatLon(c.pokemon.Lat, c.pokemon.Lon))
		if err != nil || weather == nil || !weather.GameplayCondition.Valid {
			log.Warnf("Failed to obtain weather for Pokemon %s: %s", c.pokemon.Id, err)
		} else {
			scan.CellWeather = int32(weather.GameplayCondition.Int64)
		}
	}
	if proto.CpMultiplier < 0.734 {
		scan.Level = int32((58.215688455154954*proto.CpMultiplier-2.7012478057856497)*proto.CpMultiplier + 1.3220677708486794)
	} else if proto.CpMultiplier < .795 {
		scan.Level = int32(171.34093607855277*proto.CpMultiplier - 94.95626666368578)
	} else {
		scan.Level = int32(199.99995231630976*proto.CpMultiplier - 117.55996066890287)
	}

	caughtIv, err := c.detectDitto(&scan)
	if err != nil {
		caughtIv = &scan
		log.Errorf("[POKEMON] Unexpected %s: %s", c.pokemon.Id, err)
	}
	if caughtIv == nil { // this can only happen for a 0P Ditto
		c.pokemon.Level = null.IntFrom(int64(scan.Level - 5))
		c.clearIv(false)
	} else {
		c.pokemon.Level = null.IntFrom(int64(caughtIv.Level))
		c.calculateIv(int64(caughtIv.Attack), int64(caughtIv.Defense), int64(caughtIv.Stamina))
	}
	if err == nil {
		newScans := make([]*grpc.PokemonScan, len(c.pokemon.internal.ScanHistory)+1)
		entriesCount := 0
		for _, oldEntry := range c.pokemon.internal.ScanHistory {
			if oldEntry.Strong != scan.Strong || !oldEntry.Strong &&
				oldEntry.Weather == int32(pogo.GameplayWeatherProto_NONE) !=
					(scan.Weather == int32(pogo.GameplayWeatherProto_NONE)) {
				newScans[entriesCount] = oldEntry
				entriesCount++
			}
		}
		newScans[entriesCount] = &scan
		c.pokemon.internal.ScanHistory = newScans[:entriesCount+1]

		unboosted, boosted, strong := c.locateAllScans()
		if unboosted != nil && boosted != nil {
			unboosted.RemoveDittoAuxInfo()
			boosted.RemoveDittoAuxInfo()
		}
		if strong != nil {
			strong.RemoveDittoAuxInfo()
		}
	} else {
		// undo possible changes
		scan.Confirmed = false
		scan.Weather = int32(c.pokemon.Weather.Int64)
		c.pokemon.internal.ScanHistory = make([]*grpc.PokemonScan, 1)
		c.pokemon.internal.ScanHistory[0] = &scan
	}
}

func (c *pokemonCalc) recomputeCpIfNeeded() {
	if c.pokemon.Cp.Valid || ohbem == nil {
		return
	}
	var displayPokemon int
	shouldOverrideIv := false
	var overrideIv *grpc.PokemonScan
	if c.pokemon.IsDitto {
		displayPokemon = int(c.pokemon.DisplayPokemonId.Int64)
		if c.pokemon.Weather.Int64 == int64(pogo.GameplayWeatherProto_NONE) {
			cellId := weatherCellIdFromLatLon(c.pokemon.Lat, c.pokemon.Lon)
			weather, found := c.weather[cellId]
			if !found {
				record, err := getWeatherRecord(c.ctx, c.db, cellId)
				if err != nil || record == nil || !record.GameplayCondition.Valid {
					log.Warnf("[POKEMON] Failed to obtain weather for Pokemon %s: %s", c.pokemon.Id, err)
				} else {
					log.Warnf("[POKEMON] Weather not found locally for %s at %d", c.pokemon.Id, cellId)
					weather = pogo.GameplayWeatherProto_WeatherCondition(record.GameplayCondition.Int64)
					found = true
				}
			}
			if found && weather == pogo.GameplayWeatherProto_PARTLY_CLOUDY {
				shouldOverrideIv = true
				scan, isBoostedMatches := c.locateScan(false, false)
				if scan != nil && isBoostedMatches {
					overrideIv = scan
				}
			}
		}
	} else {
		displayPokemon = int(c.pokemon.PokemonId)
	}
	var cp int
	var err error
	if shouldOverrideIv {
		if overrideIv == nil {
			return
		}
		// You should see boosted IV for 0P Ditto
		cp, err = ohbem.CalculateCp(displayPokemon, int(c.pokemon.Form.ValueOrZero()), 0,
			int(overrideIv.Attack), int(overrideIv.Defense), int(overrideIv.Stamina), float64(overrideIv.Level))
	} else {
		if !c.pokemon.AtkIv.Valid || !c.pokemon.Level.Valid {
			return
		}
		cp, err = ohbem.CalculateCp(displayPokemon, int(c.pokemon.Form.ValueOrZero()), 0,
			int(c.pokemon.AtkIv.Int64), int(c.pokemon.DefIv.Int64), int(c.pokemon.StaIv.Int64),
			float64(c.pokemon.Level.Int64))
	}
	if err == nil {
		c.pokemon.Cp = null.IntFrom(int64(cp))
	} else {
		log.Warnf("Pokemon %s %d CP unset due to error %s", c.pokemon.Id, displayPokemon, err)
	}
}
