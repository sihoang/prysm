package spectest

// func TestSlotProcessingMainnet(t *testing.T) {
// 	filepath, err := bazel.Runfile("/eth2_spec_tests/tests/sanity/slots/sanity_slots_mainnet.yaml")
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	file, err := ioutil.ReadFile(filepath)
// 	if err != nil {
// 		t.Fatalf("Could not load file %v", err)
// 	}

// 	s := &SanitySlotsTest{}
// 	if err := yaml.Unmarshal(file, s); err != nil {
// 		t.Fatalf("Failed to Unmarshal: %v", err)
// 	}

// 	if err := spectest.SetConfig(s.Config); err != nil {
// 		t.Fatal(err)
// 	}

// 	for _, tt := range s.TestCases {
// 		t.Run(tt.Description, func(t *testing.T) {
// 			postState, err := state.ProcessSlots(context.Background(), tt.Pre, tt.Pre.Slot+tt.Slots)
// 			if err != nil {
// 				t.Fatal(err)
// 			}

// 			if !proto.Equal(postState, tt.Post) {
// 				diff, _ := messagediff.PrettyDiff(postState, tt.Post)
// 				t.Log(diff)
// 				_ = diff
// 				t.Fatal("Post state does not match expected")
// 			}
// 		})
// 	}
// }
