from pathlib import Path
import re


def replace_func(text: str, name: str, replacement: str) -> str:
    marker = f"func {name}"
    start = text.index(marker)
    brace = text.index("{", start)
    depth = 0
    i = brace
    while i < len(text):
        if text[i] == "{":
            depth += 1
        elif text[i] == "}":
            depth -= 1
            if depth == 0:
                return text[:start] + replacement.rstrip() + "\n" + text[i + 1:]
        i += 1
    raise RuntimeError(f"unterminated function {name}")

app_path = Path("internal/lgit/app.go")
app = app_path.read_text()
app = app.replace("lgit init [--env NAME] [--new-project]", "lgit init [--env NAME] [--new-project] [--encryption identity|password]")
app = app.replace("env, newProject, err := parseInit(args)", "env, newProject, encryption, err := parseInit(args)")
app = app.replace("if err := a.initEncryption(root, p); err != nil {", "if err := a.initEncryption(root, p, encryption); err != nil {")

parse_init = r'''func parseInit(args []string) (string, bool, string, error) {
	env := ""
	newProject := false
	encryption := "identity"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--env":
			i++
			if i >= len(args) {
				return "", false, "", fmt.Errorf("--env requires a name")
			}
			env = args[i]
		case "--new-project":
			newProject = true
		case "--encryption":
			i++
			if i >= len(args) {
				return "", false, "", fmt.Errorf("--encryption requires identity or password")
			}
			encryption = strings.ToLower(args[i])
			if encryption != "identity" && encryption != "password" {
				return "", false, "", fmt.Errorf("--encryption must be identity or password")
			}
		default:
			return "", false, "", fmt.Errorf("usage: lgit init [--env NAME] [--new-project] [--encryption identity|password]")
		}
	}
	if env == "" {
		h, e := os.Hostname()
		if e != nil {
			return "", false, "", e
		}
		env = h
	}
	env, e := validateName(env)
	return env, newProject, encryption, e
}'''
app = replace_func(app, "parseInit(args []string) (string, bool, error)", parse_init)
app_path.write_text(app)

age_path = Path("internal/lgit/age_store.go")
age = age_path.read_text()
age = age.replace('"time"\n\n\t"filippo.io/age"', '"time"\n\n\t"filippo.io/age"\n\t"golang.org/x/term"')
age = age.replace('const ageFormat = "lgit-age-v1"', 'const (\n\tageFormat = "lgit-age-v1"\n\tagePasswordFormat = "lgit-age-scrypt-v1"\n)')

helpers = r'''
func readAgeFormat(root string) (ageFormatFile, error) {
	var f ageFormatFile
	b, err := os.ReadFile(filepath.Join(root, ".lgit", "format.json"))
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, err
	}
	if f.Version != 1 {
		return f, fmt.Errorf("unsupported lgit encryption format version %d", f.Version)
	}
	return f, nil
}

func (a App) readPassword(confirm bool) (string, error) {
	if password := os.Getenv("LGIT_PASSWORD"); password != "" {
		return password, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("password required but stdin is not a terminal; set LGIT_PASSWORD for non-interactive use")
	}
	fmt.Fprint(a.Stderr, "Encryption password: ")
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(a.Stderr)
	if err != nil {
		return "", err
	}
	if len(first) == 0 {
		return "", fmt.Errorf("password cannot be empty")
	}
	if confirm {
		fmt.Fprint(a.Stderr, "Confirm password: ")
		second, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(a.Stderr)
		if err != nil {
			return "", err
		}
		if !bytes.Equal(first, second) {
			return "", fmt.Errorf("passwords do not match")
		}
	}
	return string(first), nil
}

func (a App) encryptionRecipients(root string) ([]age.Recipient, error) {
	f, err := readAgeFormat(root)
	if err != nil {
		return nil, err
	}
	if f.Encryption == agePasswordFormat {
		password, err := a.readPassword(false)
		if err != nil {
			return nil, err
		}
		r, err := age.NewScryptRecipient(password)
		if err != nil {
			return nil, err
		}
		return []age.Recipient{r}, nil
	}
	if f.Encryption != ageFormat {
		return nil, fmt.Errorf("unsupported encryption mode %q", f.Encryption)
	}
	return readRecipients(root)
}

func (a App) decryptionIdentity(root string) (age.Identity, error) {
	f, err := readAgeFormat(root)
	if err != nil {
		return nil, err
	}
	if f.Encryption == agePasswordFormat {
		password, err := a.readPassword(false)
		if err != nil {
			return nil, err
		}
		return age.NewScryptIdentity(password)
	}
	if f.Encryption != ageFormat {
		return nil, fmt.Errorf("unsupported encryption mode %q", f.Encryption)
	}
	return a.loadIdentity()
}
'''
age = age.replace("func (a App) identityPath()", helpers + "\nfunc (a App) identityPath()")

init_encryption = r'''func (a App) initEncryption(root string, p Project, mode string) error {
	meta := filepath.Join(root, ".lgit")
	if err := os.MkdirAll(filepath.Join(meta, "store"), 0700); err != nil {
		return err
	}
	format := ageFormat
	var recipient string
	if mode == "password" {
		if _, err := a.readPassword(true); err != nil {
			return err
		}
		format = agePasswordFormat
	} else {
		id, err := a.ensureIdentity()
		if err != nil {
			return err
		}
		recipient = id.Recipient().String() + "\n"
	}
	f, _ := json.MarshalIndent(ageFormatFile{Version: 1, Encryption: format}, "", "  ")
	if err := os.WriteFile(filepath.Join(meta, "format.json"), append(f, '\n'), 0600); err != nil {
		return err
	}
	metadata := []string{".lgit/format.json"}
	if recipient != "" {
		if err := os.WriteFile(filepath.Join(meta, "recipients.txt"), []byte(recipient), 0600); err != nil {
			return err
		}
		metadata = append(metadata, ".lgit/recipients.txt")
	}
	if err := excludeLgit(root); err != nil {
		return err
	}
	args := append([]string{"add", "--force"}, metadata...)
	if c := a.run(root, p.GitDir, args...); c != 0 {
		return fmt.Errorf("failed to stage encryption metadata")
	}
	return nil
}'''
age = replace_func(age, "(a App) initEncryption(root string, p Project) error", init_encryption)

# Replace recipient/identity acquisition in encryption-aware operations.
age = age.replace("rs, err := readRecipients(root)", "rs, err := a.encryptionRecipients(root)")
for func_name in ["(a App) materialize(root string, p Project) error", "(a App) plainConflicts(root string, p Project, ref string) ([]string, error)"]:
    start = age.index("func " + func_name)
    end = age.index("\nfunc ", start + 5)
    block = age[start:end]
    block = block.replace("id, err := a.loadIdentity()", "id, err := a.decryptionIdentity(root)")
    age = age[:start] + block + age[end:]
# Remaining restore/status helpers use loadIdentity in the same file; replace only where no Recipient() call follows.
age = re.sub(r'id, err := a\.loadIdentity\(\)\n(\tif err != nil \{\n\t\treturn [^\n]+\n\t\})', r'id, err := a.decryptionIdentity(root)\n\1', age)
age_path.write_text(age)

test_path = Path("internal/lgit/age_store_test.go")
test = test_path.read_text()
test += r'''

func TestPasswordAgeRoundTrip(t *testing.T) {
	plain := []byte("SECRET=password-mode\n")
	r, err := age.NewScryptRecipient("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := encryptBytes(plain, []age.Recipient{r})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cipher, plain) {
		t.Fatal("ciphertext contains plaintext")
	}
	id, err := age.NewScryptIdentity("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	got, err := decryptBytes(cipher, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q", got)
	}
}

func TestParseInitEncryptionModes(t *testing.T) {
	_, _, mode, err := parseInit([]string{"--env", "PCX", "--encryption", "password"})
	if err != nil || mode != "password" {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
	_, _, mode, err = parseInit([]string{"--env", "PCX"})
	if err != nil || mode != "identity" {
		t.Fatalf("default mode=%q err=%v", mode, err)
	}
}
'''
test_path.write_text(test)

readme_path = Path("README.md")
readme = readme_path.read_text()
insert = '''\n## Encryption modes\n\nIdentity encryption remains the default:\n\n```bash\nlgit init --env PCX --encryption identity\n```\n\nPassword encryption avoids copying an identity file between computers:\n\n```bash\nlgit init --env PCX --encryption password\n```\n\nPassword-mode projects prompt when encrypting or decrypting. The password is never stored in the repository. For non-interactive automation, `LGIT_PASSWORD` is supported, but setting secrets through the process environment should be limited to controlled environments. Existing identity-mode repositories remain fully compatible.\n'''
if "## Encryption modes" not in readme:
    readme = readme.replace("\n## Environments\n", insert + "\n## Environments\n")
readme_path.write_text(readme)

Path("scripts/apply_password_mode.py").unlink()
