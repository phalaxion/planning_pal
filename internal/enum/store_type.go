package store_type

import "fmt"

type StoreType int

const (
	JSON StoreType = iota
	SQLITE
)

func (st StoreType) Name() string {
	if st < JSON || st > SQLITE {
		return "Unknown"
	}
	return [...]string{"json", "sqlite"}[st]
}

func (st StoreType) String() string {
	return st.Name()
}

func Values() []StoreType {
	return []StoreType{JSON, SQLITE}
}

func ValueOf(name string) (StoreType, error) {
	switch name {
	case "json":
		return JSON, nil
	case "sqlite":
		return SQLITE, nil
	default:
		return 0, fmt.Errorf("Invalid StoreType: %s", name)
	}
}
