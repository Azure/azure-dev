package slice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Find_SimpleType(t *testing.T) {
	t.Run("WithMatch", func(t *testing.T) {
		slice := []string{"a", "b", "c", "d"}
		match := Find(slice, func(value string) bool {
			return value == "c"
		})

		require.Equal(t, "c", *match)
	})

	t.Run("NoMatch", func(t *testing.T) {
		slice := []string{"a", "b", "c", "d"}
		match := Find(slice, func(value string) bool {
			return value == "e"
		})

		require.Nil(t, match)
	})
}

func Test_Find_ComplexType(t *testing.T) {
	t.Run("WithMatch", func(t *testing.T) {
		people := []*Person{
			{FirstName: "Wayne", LastName: "Gretzkey"},
			{FirstName: "Conner", LastName: "McDavid"},
			{FirstName: "Patrick", LastName: "Kane"},
			{FirstName: "Auston", LastName: "Matthews"},
		}

		match := *Find(people, func(value *Person) bool {
			return value.LastName == "Gretzkey"
		})

		require.Same(t, people[0], match)
	})

	t.Run("NoMatch", func(t *testing.T) {
		people := []*Person{
			{FirstName: "Wayne", LastName: "Gretzkey"},
			{FirstName: "Conner", LastName: "McDavid"},
			{FirstName: "Patrick", LastName: "Kane"},
			{FirstName: "Auston", LastName: "Matthews"},
		}

		match := Find(people, func(value *Person) bool {
			return value.LastName == "NoMatch"
		})

		require.Nil(t, match)
	})
}

type Person struct {
	FirstName string
	LastName  string
}
