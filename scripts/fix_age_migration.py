from pathlib import Path

root = Path(__file__).resolve().parents[1]
app = root / "internal/lgit/app.go"
s = app.read_text()
s = s.replace('args[0] != "list" && args[0] != "data-dir" && args[0] != "help"', 'args[0] != "list" && args[0] != "data-dir" && args[0] != "key" && args[0] != "help"')
app.write_text(s)

test = root / "internal/lgit/app_test.go"
s = test.read_text()

old = '''\tdata2 := filepath.Join(t.TempDir(), "data2")\n\tsecond := filepath.Join(t.TempDir(), "Booking")'''
new = '''\tdata2 := filepath.Join(t.TempDir(), "data2")\n\tif err := os.MkdirAll(data2, 0700); err != nil { t.Fatal(err) }\n\tkey, err := os.ReadFile(filepath.Join(data1, "age-identity.txt"))\n\tif err != nil { t.Fatal(err) }\n\tif err := os.WriteFile(filepath.Join(data2, "age-identity.txt"), key, 0600); err != nil { t.Fatal(err) }\n\tsecond := filepath.Join(t.TempDir(), "Booking")'''
s = s.replace(old, new, 1)

old2 = '''\td2 := filepath.Join(t.TempDir(), "d2")\n\tsecond := filepath.Join(t.TempDir(), "Booking")'''
new2 = '''\td2 := filepath.Join(t.TempDir(), "d2")\n\tif err := os.MkdirAll(d2, 0700); err != nil { t.Fatal(err) }\n\tkey, err := os.ReadFile(filepath.Join(d1, "age-identity.txt"))\n\tif err != nil { t.Fatal(err) }\n\tif err := os.WriteFile(filepath.Join(d2, "age-identity.txt"), key, 0600); err != nil { t.Fatal(err) }\n\tsecond := filepath.Join(t.TempDir(), "Booking")'''
s = s.replace(old2, new2, 1)
test.write_text(s)

Path(__file__).unlink()
