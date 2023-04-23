package decoder

import (
	"github.com/antonmedv/expr"
	"github.com/antonmedv/expr/vm"
	log "github.com/sirupsen/logrus"
	"regexp"
	"strings"
)

var filterTokenizer = regexp.MustCompile(
	`^\s*([()|&!,]|([ADSLXG]?|CP|LC|[GU]L)\s*([0-9]+(?:\.[0-9]*)?)(?:\s*-\s*([0-9]+(?:\.[0-9]*)?))?)\s*`)
var emptyPvp = PokemonPvpLookup{Little: -1, Great: -1, Ultra: -1}

type filterEnv struct {
	Pokemon *PokemonLookup
	Pvp     *PokemonPvpLookup
}
type expertFilterCache map[string]*vm.Program

func compilePokemonFilter(cache expertFilterCache, expert string) *vm.Program {
	expert = strings.ToUpper(expert)
	if out, ok := cache[expert]; ok {
		return out
	}
	out := func() *vm.Program {
		var builder strings.Builder
		// we first transcode input filter into a compilable expr
		for i := 0; i < len(expert); {
			slice := expert[i:]
			match := filterTokenizer.FindStringSubmatchIndex(slice)
			if match == nil {
				log.Debugf("Failed to transcode Pokemon expert filter @ %d: %s", i, expert)
				return nil
			}
			i += match[1]
			if match[6] < 0 {
				switch s := slice[match[2]:match[3]]; s {
				case "(", ")", "!":
					builder.WriteString(s)
				case "|", ",":
					builder.WriteString("||")
				case "&":
					builder.WriteString("&&")
				}
				continue
			}
			var column string
			switch s := slice[match[4]:match[5]]; s {
			case "":
				column = "Pokemon.Iv"
			case "A":
				column = "Pokemon.Atk"
			case "D":
				column = "Pokemon.Def"
			case "S":
				column = "Pokemon.Sta"
			case "L":
				column = "Pokemon.Level"
			case "X":
				column = "Pokemon.Size"
			case "CP":
				column = "Pokemon.Cp"
			case "GL":
				column = "Pvp.Great"
			case "UL":
				column = "Pvp.Ultra"
			case "LC":
				column = "Pvp.Little"
			default:
				panic("You forgot to update this switch after changing the tokenizer regexp!")
			}
			builder.WriteByte('(')
			builder.WriteString(column)
			if match[8] < 0 {
				builder.WriteString("==")
				builder.WriteString(slice[match[6]:match[7]])
			} else {
				builder.WriteString(">=")
				builder.WriteString(slice[match[6]:match[7]])
				builder.WriteString("&&")
				builder.WriteString(column)
				builder.WriteString("<=")
				builder.WriteString(slice[match[8]:match[9]])
			}
			builder.WriteByte(')')
		}
		if builder.Len() == 0 {
			builder.WriteString("true")
		}
		out, err := expr.Compile(builder.String(), expr.Env(filterEnv{}), expr.AsBool())
		if err != nil {
			log.Debugf("Malformed Pokemon expert filter: %s; Failed to compile %s: %s", expert, builder.String(), err)
		}
		return out
	}()
	cache[expert] = out
	return out
}
