package geo

func AreaMatchWithWildcards(areas []AreaName, areasToMatch []AreaName) bool {
	for _, hookArea := range areasToMatch {
		for _, messageArea := range areas {
			if hookArea.Name == "*" {
				if hookArea.Parent == messageArea.Parent {
					return true
				}
			} else if hookArea.Parent == "*" {
				if hookArea.Name == messageArea.Name {
					return true
				}
			} else {
				if hookArea.Parent == messageArea.Parent && hookArea.Name == messageArea.Name {
					return true
				}
			}
		}
	}
	return false
}
