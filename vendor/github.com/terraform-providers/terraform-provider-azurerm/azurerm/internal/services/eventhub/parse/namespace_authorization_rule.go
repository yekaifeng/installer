package parse

import "github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/azure"

type NamespaceAuthorizationRuleId struct {
	ResourceGroup string
	NamespaceName string
	Name          string
}

func NamespaceAuthorizationRuleID(input string) (*NamespaceAuthorizationRuleId, error) {
	id, err := azure.ParseAzureResourceID(input)
	if err != nil {
		return nil, err
	}

	rule := NamespaceAuthorizationRuleId{
		ResourceGroup: id.ResourceGroup,
	}

	if rule.NamespaceName, err = id.PopSegment("namespaces"); err != nil {
		return nil, err
	}

	if rule.Name, err = id.PopSegment("AuthorizationRules"); err != nil {
		return nil, err
	}

	if err := id.ValidateNoEmptySegments(input); err != nil {
		return nil, err
	}

	return &rule, nil
}
