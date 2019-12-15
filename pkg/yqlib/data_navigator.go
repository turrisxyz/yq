package yqlib

import (
	"bytes"
	"strconv"
	"strings"

	logging "gopkg.in/op/go-logging.v1"
	yaml "gopkg.in/yaml.v3"
)

type DataNavigator interface {
	DebugNode(node *yaml.Node)
	Get(rootNode *yaml.Node, path []string) (*yaml.Node, error)
	Update(rootNode *yaml.Node, path []string, changesToApply yaml.Node) error
	Delete(rootNode *yaml.Node, path []string) error
}

type navigator struct {
	log *logging.Logger
}

type VisitorFn func(*yaml.Node) error

func NewDataNavigator(l *logging.Logger) DataNavigator {
	return &navigator{
		log: l,
	}
}

func (n *navigator) Get(value *yaml.Node, path []string) (*yaml.Node, error) {
	matchingNodes := make([]*yaml.Node, 0)

	n.Visit(value, path, func(matchedNode *yaml.Node) error {
		matchingNodes = append(matchingNodes, matchedNode)
		n.log.Debug("Matched")
		n.DebugNode(matchedNode)
		return nil
	})
	n.log.Debug("finished iterating, found %v matches", len(matchingNodes))
	if len(matchingNodes) == 0 {
		return nil, nil
	} else if len(matchingNodes) == 1 {
		return matchingNodes[0], nil
	}
	// make a new node
	var newNode = yaml.Node{Kind: yaml.SequenceNode}
	newNode.Content = matchingNodes
	return &newNode, nil
}

func (n *navigator) Update(rootNode *yaml.Node, path []string, changesToApply yaml.Node) error {
	errorVisiting := n.Visit(rootNode, path, func(nodeToUpdate *yaml.Node) error {
		n.log.Debug("going to update")
		n.DebugNode(nodeToUpdate)
		n.log.Debug("with")
		n.DebugNode(&changesToApply)
		nodeToUpdate.Value = changesToApply.Value
		nodeToUpdate.Tag = changesToApply.Tag
		nodeToUpdate.Kind = changesToApply.Kind
		nodeToUpdate.Style = changesToApply.Style
		nodeToUpdate.Content = changesToApply.Content
		nodeToUpdate.HeadComment = changesToApply.HeadComment
		nodeToUpdate.LineComment = changesToApply.LineComment
		nodeToUpdate.FootComment = changesToApply.FootComment
		return nil
	})
	return errorVisiting
}

func (n *navigator) Delete(rootNode *yaml.Node, path []string) error {

	lastBit, newTail := path[len(path)-1], path[:len(path)-1]
	n.log.Debug("splitting path, %v", lastBit)
	n.log.Debug("new tail, %v", newTail)
	errorVisiting := n.Visit(rootNode, newTail, func(nodeToUpdate *yaml.Node) error {
		n.log.Debug("need to find %v in here", lastBit)
		n.DebugNode(nodeToUpdate)
		original := nodeToUpdate.Content
		if nodeToUpdate.Kind == yaml.SequenceNode {
			var index, err = strconv.ParseInt(lastBit, 10, 64) // nolint
			if err != nil {
				return err
			}
			if index >= int64(len(nodeToUpdate.Content)) {
				n.log.Debug("index %v is greater than content length %v", index, len(nodeToUpdate.Content))
				return nil
			}
			nodeToUpdate.Content = append(original[:index], original[index+1:]...)

		} else if nodeToUpdate.Kind == yaml.MappingNode {

			_, errorVisiting := n.visitMatchingEntries(nodeToUpdate.Content, lastBit, func(indexInMap int) error {
				nodeToUpdate.Content = append(original[:indexInMap], original[indexInMap+2:]...)
				return nil
			})
			if errorVisiting != nil {
				return errorVisiting
			}

		}

		return nil
	})
	return errorVisiting
}

func (n *navigator) Visit(value *yaml.Node, path []string, visitor VisitorFn) error {
	realValue := value
	if realValue.Kind == yaml.DocumentNode {
		n.log.Debugf("its a document! returning the first child")
		realValue = value.Content[0]
	}
	if len(path) > 0 {
		n.log.Debugf("diving into %v", path[0])
		n.DebugNode(value)
		return n.recurse(realValue, path[0], path[1:], visitor)
	}
	return visitor(realValue)
}

func (n *navigator) guessKind(tail []string, guess yaml.Kind) yaml.Kind {
	n.log.Debug("tail %v", tail)
	if len(tail) == 0 && guess == 0 {
		n.log.Debug("end of path, must be a scalar")
		return yaml.ScalarNode
	} else if len(tail) == 0 {
		return guess
	}

	var _, errorParsingInt = strconv.ParseInt(tail[0], 10, 64)
	if tail[0] == "+" || errorParsingInt == nil {
		return yaml.SequenceNode
	}
	if tail[0] == "*" && (guess == yaml.SequenceNode || guess == yaml.MappingNode) {
		return guess
	}
	return yaml.MappingNode
}

func (n *navigator) getOrReplace(original *yaml.Node, expectedKind yaml.Kind) *yaml.Node {
	if original.Kind != expectedKind {
		n.log.Debug("wanted %v but it was %v, overriding", expectedKind, original.Kind)
		return &yaml.Node{Kind: expectedKind}
	}
	return original
}

func (n *navigator) DebugNode(value *yaml.Node) {
	if value == nil {
		n.log.Debug("-- node is nil --")
	} else if n.log.IsEnabledFor(logging.DEBUG) {
		buf := new(bytes.Buffer)
		encoder := yaml.NewEncoder(buf)
		encoder.Encode(value)
		encoder.Close()
		n.log.Debug("Tag: %v", value.Tag)
		n.log.Debug("%v", buf.String())
	}
}

func (n *navigator) recurse(value *yaml.Node, head string, tail []string, visitor VisitorFn) error {
	switch value.Kind {
	case yaml.MappingNode:
		n.log.Debug("its a map with %v entries", len(value.Content)/2)
		if head == "*" {
			return n.splatMap(value, tail, visitor)
		}
		return n.recurseMap(value, head, tail, visitor)
	case yaml.SequenceNode:
		n.log.Debug("its a sequence of %v things!, %v", len(value.Content))
		if head == "*" {
			return n.splatArray(value, tail, visitor)
		} else if head == "+" {
			return n.appendArray(value, tail, visitor)
		}
		return n.recurseArray(value, head, tail, visitor)
	default:
		return nil
	}
}

func (n *navigator) splatMap(value *yaml.Node, tail []string, visitor VisitorFn) error {
	for index, content := range value.Content {
		if index%2 == 0 {
			continue
		}
		content = n.getOrReplace(content, n.guessKind(tail, content.Kind))
		var err = n.Visit(content, tail, visitor)
		if err != nil {
			return err
		}
	}
	return nil
}

func (n *navigator) recurseMap(value *yaml.Node, head string, tail []string, visitor VisitorFn) error {
	visited, errorVisiting := n.visitMatchingEntries(value.Content, head, func(indexInMap int) error {
		value.Content[indexInMap+1] = n.getOrReplace(value.Content[indexInMap+1], n.guessKind(tail, value.Content[indexInMap+1].Kind))
		return n.Visit(value.Content[indexInMap+1], tail, visitor)
	})

	if errorVisiting != nil {
		return errorVisiting
	}

	if visited {
		return nil
	}

	//didn't find it, lets add it.
	value.Content = append(value.Content, &yaml.Node{Value: head, Kind: yaml.ScalarNode})
	mapEntryValue := yaml.Node{Kind: n.guessKind(tail, 0)}
	value.Content = append(value.Content, &mapEntryValue)
	n.log.Debug("adding new node %v", value.Content)
	return n.Visit(&mapEntryValue, tail, visitor)
}

type mapVisitorFn func(int) error

func (n *navigator) visitMatchingEntries(contents []*yaml.Node, key string, visit mapVisitorFn) (bool, error) {
	visited := false
	for index, content := range contents {
		// value.Content is a concatenated array of key, value,
		// so keys are in the even indexes, values in odd.
		if index%2 == 0 && (n.matchesKey(key, content.Value)) {
			errorVisiting := visit(index)
			if errorVisiting != nil {
				return visited, errorVisiting
			}
			visited = true
		}
	}
	return visited, nil
}

func (n *navigator) matchesKey(key string, actual string) bool {
	var prefixMatch = strings.TrimSuffix(key, "*")
	if prefixMatch != key {
		return strings.HasPrefix(actual, prefixMatch)
	}
	return actual == key
}

func (n *navigator) splatArray(value *yaml.Node, tail []string, visitor VisitorFn) error {
	for _, childValue := range value.Content {
		n.log.Debug("processing")
		n.DebugNode(childValue)
		childValue = n.getOrReplace(childValue, n.guessKind(tail, childValue.Kind))
		var err = n.Visit(childValue, tail, visitor)
		if err != nil {
			return err
		}
	}
	return nil
}

func (n *navigator) appendArray(value *yaml.Node, tail []string, visitor VisitorFn) error {
	var newNode = yaml.Node{Kind: n.guessKind(tail, 0)}
	value.Content = append(value.Content, &newNode)
	n.log.Debug("appending a new node, %v", value.Content)
	return n.Visit(&newNode, tail, visitor)
}

func (n *navigator) recurseArray(value *yaml.Node, head string, tail []string, visitor VisitorFn) error {
	var index, err = strconv.ParseInt(head, 10, 64) // nolint
	if err != nil {
		return err
	}
	if index >= int64(len(value.Content)) {
		return nil
	}
	value.Content[index] = n.getOrReplace(value.Content[index], n.guessKind(tail, value.Content[index].Kind))
	return n.Visit(value.Content[index], tail, visitor)
}

// func entriesInSlice(context yaml.MapSlice, key string) []*yaml.MapItem {
// 	var matches = make([]*yaml.MapItem, 0)
// 	for idx := range context {
// 		var entry = &context[idx]
// 		if matchesKey(key, entry.Key) {
// 			matches = append(matches, entry)
// 		}
// 	}
// 	return matches
// }

// func getMapSlice(context interface{}) yaml.MapSlice {
// 	var mapSlice yaml.MapSlice
// 	switch context := context.(type) {
// 	case yaml.MapSlice:
// 		mapSlice = context
// 	default:
// 		mapSlice = make(yaml.MapSlice, 0)
// 	}
// 	return mapSlice
// }

// func getArray(context interface{}) (array []interface{}, ok bool) {
// 	switch context := context.(type) {
// 	case []interface{}:
// 		array = context
// 		ok = true
// 	default:
// 		array = make([]interface{}, 0)
// 		ok = false
// 	}
// 	return
// }

// func writeMap(context interface{}, paths []string, value interface{}) interface{} {
// 	log.Debugf("writeMap with path %v for %v to set value %v\n", paths, context, value)

// 	mapSlice := getMapSlice(context)

// 	if len(paths) == 0 {
// 		return context
// 	}

// 	children := entriesInSlice(mapSlice, paths[0])

// 	if len(children) == 0 && paths[0] == "*" {
// 		log.Debugf("\tNo matches, return map as is")
// 		return context
// 	}

// 	if len(children) == 0 {
// 		newChild := yaml.MapItem{Key: paths[0]}
// 		mapSlice = append(mapSlice, newChild)
// 		children = entriesInSlice(mapSlice, paths[0])
// 		log.Debugf("\tAppended child at %v for mapSlice %v\n", paths[0], mapSlice)
// 	}

// 	remainingPaths := paths[1:]
// 	for _, child := range children {
// 		child.Value = updatedChildValue(child.Value, remainingPaths, value)
// 	}
// 	log.Debugf("\tReturning mapSlice %v\n", mapSlice)
// 	return mapSlice
// }

// func updatedChildValue(child interface{}, remainingPaths []string, value interface{}) interface{} {
// 	if len(remainingPaths) == 0 {
// 		return value
// 	}
// 	log.Debugf("updatedChildValue for child %v with path %v to set value %v", child, remainingPaths, value)
// 	log.Debugf("type of child is %v", reflect.TypeOf(child))

// 	switch child := child.(type) {
// 	case nil:
// 		if remainingPaths[0] == "+" || remainingPaths[0] == "*" {
// 			return writeArray(child, remainingPaths, value)
// 		}
// 	case []interface{}:
// 		_, nextIndexErr := strconv.ParseInt(remainingPaths[0], 10, 64)
// 		arrayCommand := nextIndexErr == nil || remainingPaths[0] == "+" || remainingPaths[0] == "*"
// 		if arrayCommand {
// 			return writeArray(child, remainingPaths, value)
// 		}
// 	}
// 	return writeMap(child, remainingPaths, value)
// }

// func writeArray(context interface{}, paths []string, value interface{}) []interface{} {
// 	log.Debugf("writeArray with path %v for %v to set value %v\n", paths, context, value)
// 	array, _ := getArray(context)

// 	if len(paths) == 0 {
// 		return array
// 	}

// 	log.Debugf("\tarray %v\n", array)

// 	rawIndex := paths[0]
// 	remainingPaths := paths[1:]
// 	var index int64
// 	// the append array indicator
// 	if rawIndex == "+" {
// 		index = int64(len(array))
// 	} else if rawIndex == "*" {
// 		for index, oldChild := range array {
// 			array[index] = updatedChildValue(oldChild, remainingPaths, value)
// 		}
// 		return array
// 	} else {
// 		index, _ = strconv.ParseInt(rawIndex, 10, 64) // nolint
// 		// writeArray is only called by updatedChildValue which handles parsing the
// 		// index, as such this renders this dead code.
// 	}

// 	for index >= int64(len(array)) {
// 		array = append(array, nil)
// 	}
// 	currentChild := array[index]

// 	log.Debugf("\tcurrentChild %v\n", currentChild)

// 	array[index] = updatedChildValue(currentChild, remainingPaths, value)
// 	log.Debugf("\tReturning array %v\n", array)
// 	return array
// }

// func readMap(context yaml.MapSlice, head string, tail []string) (interface{}, error) {
// 	log.Debugf("readingMap %v with key %v\n", context, head)
// 	if head == "*" {
// 		return readMapSplat(context, tail)
// 	}

// 	entries := entriesInSlice(context, head)
// 	if len(entries) == 1 {
// 		return calculateValue(entries[0].Value, tail)
// 	} else if len(entries) == 0 {
// 		return nil, nil
// 	}
// 	var errInIdx error
// 	values := make([]interface{}, len(entries))
// 	for idx, entry := range entries {
// 		values[idx], errInIdx = calculateValue(entry.Value, tail)
// 		if errInIdx != nil {
// 			log.Errorf("Error updating index %v in %v", idx, context)
// 			return nil, errInIdx
// 		}

// 	}
// 	return values, nil
// }

// func readMapSplat(context yaml.MapSlice, tail []string) (interface{}, error) {
// 	var newArray = make([]interface{}, len(context))
// 	var i = 0
// 	for _, entry := range context {
// 		if len(tail) > 0 {
// 			val, err := recurse(entry.Value, tail[0], tail[1:])
// 			if err != nil {
// 				return nil, err
// 			}
// 			newArray[i] = val
// 		} else {
// 			newArray[i] = entry.Value
// 		}
// 		i++
// 	}
// 	return newArray, nil
// }

// func recurse(value interface{}, head string, tail []string) (interface{}, error) {
// 	switch value := value.(type) {
// 	case []interface{}:
// 		if head == "*" {
// 			return readArraySplat(value, tail)
// 		}
// 		index, err := strconv.ParseInt(head, 10, 64)
// 		if err != nil {
// 			return nil, fmt.Errorf("error accessing array: %v", err)
// 		}
// 		return readArray(value, index, tail)
// 	case yaml.MapSlice:
// 		return readMap(value, head, tail)
// 	default:
// 		return nil, nil
// 	}
// }

// func readArray(array []interface{}, head int64, tail []string) (interface{}, error) {
// 	if head >= int64(len(array)) {
// 		return nil, nil
// 	}

// 	value := array[head]
// 	return calculateValue(value, tail)
// }

// func readArraySplat(array []interface{}, tail []string) (interface{}, error) {
// 	var newArray = make([]interface{}, len(array))
// 	for index, value := range array {
// 		val, err := calculateValue(value, tail)
// 		if err != nil {
// 			return nil, err
// 		}
// 		newArray[index] = val
// 	}
// 	return newArray, nil
// }

// func calculateValue(value interface{}, tail []string) (interface{}, error) {
// 	if len(tail) > 0 {
// 		return recurse(value, tail[0], tail[1:])
// 	}
// 	return value, nil
// }

// func deleteMap(context interface{}, paths []string) (yaml.MapSlice, error) {
// 	log.Debugf("deleteMap for %v for %v\n", paths, context)

// 	mapSlice := getMapSlice(context)

// 	if len(paths) == 0 {
// 		return mapSlice, nil
// 	}

// 	var index int
// 	var child yaml.MapItem
// 	for index, child = range mapSlice {
// 		if matchesKey(paths[0], child.Key) {
// 			log.Debugf("\tMatched [%v] with [%v] at index %v", paths[0], child.Key, index)
// 			var badDelete error
// 			mapSlice, badDelete = deleteEntryInMap(mapSlice, child, index, paths)
// 			if badDelete != nil {
// 				return nil, badDelete
// 			}
// 		}
// 	}

// 	return mapSlice, nil

// }

// func deleteEntryInMap(original yaml.MapSlice, child yaml.MapItem, index int, paths []string) (yaml.MapSlice, error) {
// 	remainingPaths := paths[1:]

// 	var newSlice yaml.MapSlice
// 	if len(remainingPaths) > 0 {
// 		newChild := yaml.MapItem{Key: child.Key}
// 		var errorDeleting error
// 		newChild.Value, errorDeleting = deleteChildValue(child.Value, remainingPaths)
// 		if errorDeleting != nil {
// 			return nil, errorDeleting
// 		}

// 		newSlice = make(yaml.MapSlice, len(original))
// 		for i := range original {
// 			item := original[i]
// 			if i == index {
// 				item = newChild
// 			}
// 			newSlice[i] = item
// 		}
// 	} else {
// 		// Delete item from slice at index
// 		newSlice = append(original[:index], original[index+1:]...)
// 		log.Debugf("\tDeleted item index %d from original", index)
// 	}

// 	log.Debugf("\tReturning original %v\n", original)
// 	return newSlice, nil
// }

// func deleteArraySplat(array []interface{}, tail []string) (interface{}, error) {
// 	log.Debugf("deleteArraySplat for %v for %v\n", tail, array)
// 	var newArray = make([]interface{}, len(array))
// 	for index, value := range array {
// 		val, err := deleteChildValue(value, tail)
// 		if err != nil {
// 			return nil, err
// 		}
// 		newArray[index] = val
// 	}
// 	return newArray, nil
// }

// func deleteArray(array []interface{}, paths []string, index int64) (interface{}, error) {
// 	log.Debugf("deleteArray for %v for %v\n", paths, array)

// 	if index >= int64(len(array)) {
// 		return array, nil
// 	}

// 	remainingPaths := paths[1:]
// 	if len(remainingPaths) > 0 {
// 		// Recurse into the array element at index
// 		var errorDeleting error
// 		array[index], errorDeleting = deleteMap(array[index], remainingPaths)
// 		if errorDeleting != nil {
// 			return nil, errorDeleting
// 		}

// 	} else {
// 		// Delete the array element at index
// 		array = append(array[:index], array[index+1:]...)
// 		log.Debugf("\tDeleted item index %d from array, leaving %v", index, array)
// 	}

// 	log.Debugf("\tReturning array: %v\n", array)
// 	return array, nil
// }

// func deleteChildValue(child interface{}, remainingPaths []string) (interface{}, error) {
// 	log.Debugf("deleteChildValue for %v for %v\n", remainingPaths, child)
// 	var head = remainingPaths[0]
// 	var tail = remainingPaths[1:]
// 	switch child := child.(type) {
// 	case yaml.MapSlice:
// 		return deleteMap(child, remainingPaths)
// 	case []interface{}:
// 		if head == "*" {
// 			return deleteArraySplat(child, tail)
// 		}
// 		index, err := strconv.ParseInt(head, 10, 64)
// 		if err != nil {
// 			return nil, fmt.Errorf("error accessing array: %v", err)
// 		}
// 		return deleteArray(child, remainingPaths, index)
// 	}
// 	return child, nil
// }