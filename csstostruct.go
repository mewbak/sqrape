/*Package sqrape provides a way to fill struct objects from raw HTML using CSS struct tags. */
package sqrape

import (
	"errors"
	"io"
	"reflect"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/fatih/structs"
	"github.com/mitchellh/mapstructure"
	"github.com/oleiade/reflections"
)

type cssStructer struct {
	// Where the data will be sent.
	targetStruct interface{}
	// Map struct field names to their intended values
	collectedFieldValues map[string]interface{}
}

func (cs *cssStructer) GetValue() error {
	//return mapstructure.Decode(cs.collectedFieldValues, cs.targetStruct) /*
	return mapstructure.WeakDecode(cs.collectedFieldValues, cs.targetStruct) //*/
}

func (cs *cssStructer) parseTargetFields(resp *goquery.Selection) error {
	//fmt.Printf("parseTargetFields: Getting csss tags.\n")
	structTags, err := reflections.Tags(cs.targetStruct, "csss")
	if err != nil {
		return err
	}
	//fmt.Printf("parseTargetFields: Iterating fieldName,fieldTag\n")
	for fieldName, fieldTag := range structTags {
		//fmt.Printf("parseTargetFields: On fieldName='%s',fieldTag='%s'\n", fieldName, fieldTag)
		err = cs.parseField(fieldName, fieldTag, resp)
		if err != nil {
			//fmt.Printf("parseTargetFields: Error on fieldName='%s',fieldTag='%s': %+v", fieldName, fieldTag, err)
			return err
		}
	}
	return nil
}

// css tags are expected to be of form "<css rules>;<attr=attrName/text/html>",
// where the latter portion determines what value is extracted.
func (cs *cssStructer) parseField(fieldName, tag string, resp *goquery.Selection) error {
	//fmt.Printf("parseField: Parsing tag='%s'\n", tag)
	var sel *goquery.Selection
	selector, valueType, attrName, err := parseTag(tag)
	if err != nil {
		return err
	}
	//fmt.Printf("parseField: Applying selector '%s' to resp\n", selector)
	if selector == "" {
		sel = resp
	} else {
		sel = resp.Find(selector)
	}
	//fmt.Printf("parseField: passing selection to setFieldValueByType")
	return cs.setFieldValueByType(valueType, fieldName, attrName, sel)
}

func (cs *cssStructer) setFieldValueByType(fieldValue, fieldName, attrName string, sel *goquery.Selection) error {
	//fmt.Printf("setFieldValueByType: Getting fieldKind for fieldName='%s'\n", fieldName)
	fieldKind, err := reflections.GetFieldKind(cs.targetStruct, fieldName)
	if err != nil {
		return err
	}
	//fmt.Printf("setFieldValueByType: fieldName='%s', fieldKind='%s'\n", fieldName, fieldKind)
	switch fieldKind {
	//case reflect.Map: // ?
	case reflect.Struct:
		{
			targetFieldDirect, targetPointer := getFieldAndPointer(cs.targetStruct, fieldName)
			err = extractByTags(sel, targetPointer)
			if err != nil {
				return err
			}
			// Convert the struct to a map again, for Mapstructure.
			//fmt.Printf("setFieldValueByType: fieldName='%s', got struct field Value: %+v\n", fieldName, targetFieldDirect)
			cs.collectedFieldValues[fieldName] = structs.Map(targetFieldDirect)
		}
	case reflect.Array, reflect.Slice:
		// Now also need to handle this for slices of structs.
		// reflect.Type's Elem() method returns the Type of slice/array contents.
		// So, can get field type, and if a type natively convertable by mapstruture
		// then continue with []string. Otherwise, manually create/allocate
		// slices and then convert to something mapstructure will use, probably
		// slice of maps.
		{
			//fmt.Printf("setFieldValueByType: fieldName='%s', slice/array field; determining type.\n", fieldName)
			selarray := make([]interface{}, 0, sel.Length())

			sliceValue, err := reflections.GetField(cs.targetStruct, fieldName)
			if err != nil {
				return err
			}
			sliceOfType := reflect.ValueOf(sliceValue).Type().Elem()
			sliceKind := sliceOfType.Kind()
			//fmt.Printf("setFieldValueByType: fieldName='%s', slice subtype is '%s'\n", fieldName, sliceKind)
			switch sliceKind {
			case reflect.Struct:
				{
					// Handle struct slices
					// Make a new object: reflect.New(sliceOfType)
					//logHTML, _ := sel.Html()
					//fmt.Printf("setFieldValueByType: fieldName='%s', Iteratively converting selection (length %d) to structs: %s\n", fieldName, sel.Length(), logHTML)
					sel.Each(func(idx int, el *goquery.Selection) {
						//logHTML, _ := el.Html()
						//fmt.Printf("setFieldValueByType.sel.Each: fieldName='%s', on el = '%s'\n", fieldName, logHTML)
						val := reflect.New(sliceOfType)
						err = extractByTags(el, val.Interface())
						if err != nil {
							//fmt.Printf("setFieldValueByType.sel.Each: fieldName='%s', Error: %+v\n", fieldName, err)
							return
						}
						//fmt.Printf("setFieldValueByType.sel.Each: fieldName='%s', mapping new value (struct?=%v): %+v\n", fieldName, structs.IsStruct(val), val)
						mval, err := reflections.Items(val.Interface())
						if err != nil {
							//fmt.Printf("setFieldValueByType.sel.Each: fieldName='%s', Error: %+v\n", fieldName, err)
							return
						}
						//fmt.Printf("setFieldValueByType.sel.Each: fieldName='%s', appending new value: %+v\n", fieldName, mval)
						selarray = append(selarray, mval)
					})
				}
			case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16,
				reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8,
				reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
				reflect.Float32, reflect.Float64:
				{
					// Handle basic type slices
					// Basic, stringified types mapstructure likes.
					sel.Each(func(idx int, el *goquery.Selection) {
						val, err := getFieldValue(fieldValue, attrName, el)
						if err != nil {
							return
						}
						selarray = append(selarray, val)
					})
				}
			default:
				return errors.New("Field is of an unsupported slice value kind: " + sliceKind.String())
			}
			//fmt.Printf("setFieldValueByType: assigning to collectedFieldValues[%s]: %+v\n", fieldName, selarray)
			cs.collectedFieldValues[fieldName] = selarray
		}
	default:
		{
			val, err := getFieldValue(fieldValue, attrName, sel)
			if err != nil {
				return err
			}
			cs.collectedFieldValues[fieldName] = val
		}
	}
	return nil
}

// Will panic if given a bad fieldName, etc.
func getFieldAndPointer(thing interface{}, fieldName string) (direct reflect.Value, pointer interface{}) {
	// reflect.Indirect returns the value of a reflect.Value, meaning it
	// derefs it *if* it's a pointer. Calling Addr() gets a pointer, and
	// interface() converts it to an interface. Simple?
	targetDirect := reflect.Indirect(reflect.ValueOf(thing))
	targetFieldDirect := reflect.Indirect(targetDirect.FieldByName(fieldName))
	targetPointer := targetFieldDirect.Addr().Interface()
	return targetFieldDirect, targetPointer
}

func getFieldValue(fieldValue, attrName string, sel *goquery.Selection) (string, error) {
	switch fieldValue {
	case "text":
		val := sel.Text()
		return val, nil
	case "html":
		return sel.Html()
	case "attr":
		val, ok := sel.Attr(attrName)
		if !ok {
			outHTML, _ := sel.Html()
			return "", errors.New("Attribute '" + attrName + "' not found in selection: " + outHTML)
		}
		return val, nil
	default:
		panic("Bad fieldValue: " + fieldValue)
	}
}

func parseTag(tag string) (selector, valueType, attrName string, err error) {
	bits := strings.Split(strings.TrimSpace(tag), ";")
	if len(bits) != 2 {
		return "", "", "", errors.New("Failed to split tag: " + tag)
	}
	selector = bits[0]
	if strings.HasPrefix(bits[1], "obj") {
		return selector, valueType, "", nil
	}
	if strings.HasPrefix(bits[1], "attr") {
		bits2 := strings.Split(strings.TrimSpace(bits[1]), "=")
		if len(bits2) < 2 {
			return "", "", "", errors.New("Failed to split attribute in tag: " + tag)
		}
		valueType = "attr"
		attrName = bits2[1]
		return selector, valueType, attrName, nil
	}
	if bits[1] != "text" && bits[1] != "html" {
		return "", "", "", errors.New("Invalid valueType, must be one of attr/text/html/obj: " + tag)
	}
	valueType = bits[1]
	return selector, valueType, "", nil
}

func prepStructer(src *goquery.Selection, dest interface{}) (*cssStructer, error) {
	cs := &cssStructer{
		targetStruct:         dest,
		collectedFieldValues: make(map[string]interface{}),
	}
	//fmt.Printf("extractByTags: passing to parseTargetFields(), state= %+v\n", cs)
	err := cs.parseTargetFields(src)
	if err != nil {
		return nil, err
	}
	return cs, nil
}

// extractByTags tries to pull information from src according to css rules in
// dest's struct field tags.
func extractByTags(src *goquery.Selection, dest interface{}) error {
	cs, err := prepStructer(src, dest)
	if err != nil {
		return err
	}
	//fmt.Printf("extractByTags: passing to GetValue(), state= %+v\n", cs)
	return cs.GetValue()
}

// mapFromTags uses template's tags to find and pull data from src, and returns
// a map of the resulting data.
func mapFromTags(src *goquery.Selection, template interface{}) (map[string]interface{}, error) {
	cs, err := prepStructer(src, template)
	if err != nil {
		return nil, err
	}
	return cs.collectedFieldValues, nil
}

// ExtractHTMLReader provides an entry point for parsing a HTML document
// in reader-form into a destination struct.
func ExtractHTMLReader(reader io.Reader, dest interface{}) error {
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return err
	}
	return extractByTags(doc.Selection, dest)
}

// ExtractHTMLString provides an entry point for parsing a HTML document
func ExtractHTMLString(document string, dest interface{}) error {
	return ExtractHTMLReader(strings.NewReader(document), dest)
}