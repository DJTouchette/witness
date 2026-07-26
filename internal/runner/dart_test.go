package runner

import (
	"reflect"
	"strings"
	"testing"
)

func TestFormatCommand_Dart(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		paths []string
		want  [][]string
	}{
		{
			name: "dart test takes the paths",
			files: map[string]string{
				"pubspec.yaml": "name: shop\nenvironment:\n  sdk: '>=3.0.0 <4.0.0'\ndev_dependencies:\n  test: ^1.24.0\n",
			},
			paths: []string{"test/order_test.dart", "test/cart_test.dart"},
			want:  [][]string{{"dart", "test", "test/order_test.dart", "test/cart_test.dart"}},
		},
		{
			// `dart test` in a Flutter package fails outright ("Flutter users
			// should run flutter test"), so the front end has to be derived.
			name: "a flutter package uses flutter test",
			files: map[string]string{
				"pubspec.yaml": "name: shop\ndependencies:\n  flutter:\n    sdk: flutter\ndev_dependencies:\n  flutter_test:\n    sdk: flutter\n",
			},
			paths: []string{"test/widget_test.dart"},
			want:  [][]string{{"flutter", "test", "test/widget_test.dart"}},
		},
		{
			name: "a top-level flutter section also marks a flutter package",
			files: map[string]string{
				"pubspec.yaml": "name: shop\nflutter:\n  uses-material-design: true\n",
			},
			paths: []string{"test/widget_test.dart"},
			want:  [][]string{{"flutter", "test", "test/widget_test.dart"}},
		},
		{
			// "flutter" appearing as part of another key or a description must
			// not switch the front end.
			name: "a mention of flutter is not a flutter package",
			files: map[string]string{
				"pubspec.yaml": "name: shop\ndescription: a client for flutter apps\ndependencies:\n  flutter_lints: ^3.0.0\n",
			},
			paths: []string{"test/order_test.dart"},
			want:  [][]string{{"dart", "test", "test/order_test.dart"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			got := argvsOf(mustFormat(t, root, "dart", tt.paths))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatCommand(dart) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatCommand_DartRefusesWithoutPubspec(t *testing.T) {
	root := writeTree(t, map[string]string{"test/order_test.dart": "void main() {}\n"})
	err := mustNoRunner(t, root, "dart", []string{"test/order_test.dart"})
	if !strings.Contains(err.Reason, "pubspec.yaml") {
		t.Errorf("Reason = %q, want it to mention the missing pubspec.yaml", err.Reason)
	}
}
