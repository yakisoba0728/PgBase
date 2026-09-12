package accessrule

type PocketBaseRules struct {
	List   *string `json:"listRule"`
	View   *string `json:"viewRule"`
	Create *string `json:"createRule"`
	Update *string `json:"updateRule"`
	Delete *string `json:"deleteRule"`
}

func FromPocketBaseRules(r PocketBaseRules) (Rules, error) {
	return FromPocketBaseRulesParsed(r, nil)
}

func FromPocketBaseRulesParsed(r PocketBaseRules, parse func(string) (Rule, error)) (Rules, error) {
	convert := func(value *string) (Rule, error) {
		if value == nil {
			return Rule{Op: "deny"}, nil
		}
		if *value == "" {
			return Rule{Op: "allow"}, nil
		}
		if *value == "@request.auth.id != ''" {
			copy := *value
			return Rule{Op: "authenticated", PocketBase: &copy}, nil
		}
		if parse != nil {
			return parse(*value)
		}
		return Rule{}, ErrInvalid
	}
	values := []*string{r.List, r.View, r.Create, r.Update, r.Delete}
	rules := make([]Rule, 5)
	for i := range values {
		rule, err := convert(values[i])
		if err != nil {
			return Rules{}, err
		}
		rules[i] = rule
	}
	return Rules{List: rules[0], View: rules[1], Create: rules[2], Update: rules[3], Delete: rules[4]}, nil
}

func (r Rules) PocketBase() PocketBaseRules {
	convert := func(rule Rule) *string {
		if rule.PocketBase != nil {
			value := *rule.PocketBase
			return &value
		}
		value := ""
		switch rule.Op {
		case "allow":
			return &value
		case "authenticated":
			value = "@request.auth.id != ''"
			return &value
		default:
			return nil
		}
	}
	return PocketBaseRules{List: convert(r.List), View: convert(r.View), Create: convert(r.Create), Update: convert(r.Update), Delete: convert(r.Delete)}
}
