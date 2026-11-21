package hook

import (
	"strings"
	"testing"
)

// coverageRewriteCases are the rows and streaming refusals added with the
// coverage expansion, positive AND negative: every program that gained a row has
// a wrapped case, and every streaming/interactive spelling has a declined one.
// They are appended to TestRewrite's table (and so to FuzzRewrite's seeds).
func coverageRewriteCases() []rewriteTestCase {
	var cases []rewriteTestCase
	wrap := func(cmd string) { cases = append(cases, rewriteTestCase{"wrap " + cmd, cmd, "sctx " + cmd, true}) }
	keep := func(cmd string) { cases = append(cases, rewriteTestCase{"decline " + cmd, cmd, cmd, false}) }
	as := func(cmd, want string) { cases = append(cases, rewriteTestCase{"rewrite " + cmd, cmd, want, true}) }

	// core: bare env is a program; env with operands stays a wrapper.
	wrap("env")
	as("env | head", "sctx env | head")
	as("env FOO=1 go test ./...", "env FOO=1 sctx go test ./...")
	keep("env -i go test ./...")
	keep("env FOO=1")
	keep("env PATH=/x go test ./...")
	wrap("printenv HOME")
	wrap("lsof -i :8080")
	keep("lsof -r 5")
	keep("lsof +r1")
	keep("lsof -i -r")
	wrap("wc -l go.mod")

	// Go extras and Rust.
	wrap("go install ./cmd/sctx")
	wrap("gotestsum --format testname ./...")
	keep("gotestsum --watch ./...")
	keep("gotestsum -w")
	wrap("govulncheck ./...")
	wrap("staticcheck ./...")
	wrap("buf lint")
	wrap("buf generate")
	keep("buf format")
	wrap("cargo doc")
	wrap("cargo install ripgrep")
	wrap("cargo nextest run")
	keep("cargo watch -x test")
	wrap("cargo run")

	// IaC and cloud.
	wrap("ansible-playbook site.yml")
	keep("ansible-playbook -k site.yml")
	keep("ansible-playbook -K site.yml")
	keep("ansible-playbook --ask-vault-pass site.yml")
	keep("ansible-playbook --ask-become-pass site.yml")
	wrap("terraform plan")
	wrap("terraform -chdir=infra plan")
	keep("terraform apply")
	keep("terraform -chdir=infra apply")
	wrap("terraform apply -auto-approve")
	wrap("terraform apply -input=false tfplan")
	keep("terraform destroy")
	wrap("terraform destroy -auto-approve")
	keep("tofu apply")
	wrap("tofu apply -auto-approve")
	keep("terraform console")
	keep("tofu console")
	wrap("terragrunt plan")
	keep("terragrunt run-all apply")
	wrap("terragrunt apply --terragrunt-non-interactive")
	as("env FOO=1 terraform plan", "env FOO=1 sctx terraform plan")
	keep("env FOO=1 terraform apply")
	wrap("aws s3 ls")
	wrap("aws logs tail /aws/lambda/x")
	keep("aws logs tail /aws/lambda/x --follow")
	keep("aws --profile p logs tail g -f")
	keep("aws ssm start-session --target i-1")
	keep("aws ecs execute-command --cluster c --task t --interactive --command sh")
	keep("aws sso login")
	keep("aws configure")
	keep("aws configure list")
	wrap("gcloud compute instances list")
	keep("gcloud logging tail")
	keep("gcloud app logs tail")
	keep("gcloud compute ssh vm")
	keep("gcloud compute start-iap-tunnel vm 22")
	keep("gcloud auth login")
	keep("gcloud run services logs read x --follow")
	keep("gcloud compute instances list --watch")
	wrap("az group list")
	keep("az webapp log tail -n x -g y")
	keep("az login")
	keep("az container logs -n x --follow")
	keep("az vm list --watch")
	keep("pulumi watch")
	keep("pulumi logs --follow")
	keep("pulumi logs -f")
	wrap("pulumi logs")
	wrap("pulumi preview")
	wrap("helm list")

	// JavaScript / TypeScript.
	wrap("playwright test")
	wrap("playwright test --headed")
	keep("playwright test --ui")
	keep("playwright test --debug")
	keep("playwright test --ui-port=9323")
	wrap("npx playwright test")
	keep("npx playwright test --ui")
	keep("pnpm exec playwright test --ui")
	wrap("pnpm exec turbo build")
	wrap("prettier --check .")
	wrap("next build")
	wrap("prisma generate")
	wrap("prisma migrate deploy")
	wrap("prisma db push")
	keep("prisma generate --watch")
	keep("prisma studio")
	keep("prisma migrate dev")
	wrap("biome check .")
	wrap("biome lint src")
	wrap("oxlint .")
	wrap("turbo run build")
	keep("turbo run dev")
	keep("turbo build --watch")
	keep("turbo test --persistent")
	keep("turbo run storybook")
	wrap("nx run-many -t build")
	keep("nx run-many -t serve")
	keep("nx run app:serve")
	keep("nx run app:dev")
	keep("nx affected --target=serve")
	keep("nx build --watch")
	wrap("nx build app")
	wrap("nx affected -t test")
	wrap("bun test")
	keep("bun test --watch")
	keep("bun test --hot")
	wrap("deno test")
	keep("deno test --watch")
	keep("deno lint --watch=src")
	keep("deno test --hot")
	wrap("deno check main.ts")

	// Linters and containers.
	wrap("shellcheck x.sh")
	wrap("hadolint Dockerfile")
	wrap("yamllint .")
	wrap("markdownlint .")
	wrap("actionlint")
	wrap("pre-commit run --all-files")
	keep("pre-commit install")
	wrap("docker buildx build .")
	wrap("docker builder build .")
	keep("docker buildx bake")
	keep("docker builder prune")
	keep("docker compose up")
	wrap("docker compose up -d")
	wrap("docker compose up --wait")
	wrap("podman ps")
	wrap("podman logs c")
	keep("podman logs -f c")
	keep("podman stats")
	wrap("podman stats --no-stream")
	keep("podman exec -it c sh")
	wrap("podman exec c ls")
	keep("podman compose up")
	keep("gh run watch 1")

	// Native toolchains and task runners.
	wrap("gcc -o x x.c")
	wrap("clang++ x.cpp")
	wrap("cc x.c")
	wrap("cmake --build build")
	wrap("ctest")
	wrap("ninja")
	wrap("ping -c 4 host")
	wrap("ping -c4 host")
	wrap("ping -w 3 host")
	keep("ping host")
	keep("ping -W 1 host")
	keep("ping -t 5 host")
	keep("timeout 60 ping 8.8.8.8")
	wrap("swift build")
	wrap("swift test")
	keep("swift run")
	wrap("ast-grep scan")
	wrap("sg scan")
	keep("ast-grep scan -i")
	keep("just")
	wrap("just build")
	wrap("just test ./pkg")
	wrap("just --list")
	keep("just dev")
	keep("just build deploy")
	keep("just build --watch")
	keep("task")
	wrap("task build")
	keep("task dev")
	keep("task serve")
	wrap("mise run build")
	wrap("mise r test")
	keep("mise run")
	keep("mise run dev")

	// Python.
	wrap("pdm install")
	wrap("pip-compile requirements.in")
	wrap("pyright")
	keep("pyright --watch")
	keep("pyright -w")
	wrap("basedpyright")
	wrap("ty check")
	keep("ty check --watch")
	keep("ty check -W")
	wrap("black .")
	keep("black --watch .")
	wrap("isort .")
	wrap("flake8")
	wrap("pylint pkg")
	wrap("sqlfluff lint q.sql")
	keep("sqlfluff fix q.sql")
	wrap("sqlfluff fix -f q.sql")
	wrap("unittest")
	wrap("python -m unittest discover")
	wrap("python -m black .")
	keep("python -m pyright --watch")
	wrap("uv remove x")
	wrap("poetry remove x")
	wrap("ruff check .")
	keep("ruff check --watch")
	keep("ruff check -w .")
	wrap("pytest")
	keep("pytest -f")
	keep("pytest --looponfail")
	keep("uv run pytest -f")

	// Ruby and PHP.
	wrap("rspec")
	wrap("rubocop")
	keep("rubocop --lsp")
	wrap("rake test")
	wrap("rails test")
	wrap("bundle exec rspec")
	keep("bundle exec rails server")
	keep("bundle exec rails s")
	keep("bundle exec puma")
	keep("bundle exec guard")
	wrap("bundle add x")
	wrap("bundle remove x")
	wrap("composer remove x")
	wrap("phpunit")
	wrap("phpstan analyse")
	keep("phpstan --watch")

	// JVM and .NET.
	wrap("mvn test")
	wrap("mvn clean install")
	keep("mvn spring-boot:run")
	keep("mvn -q spring-boot:run")
	keep("mvn quarkus:dev")
	keep("mvn jetty:run")
	keep("mvn exec:java")
	keep("mvn archetype:generate")
	wrap("mvn -B archetype:generate")
	keep("mvn release:prepare")
	wrap("mvn release:prepare -B")
	wrap("mvnw test")
	wrap("./mvnw clean verify")
	wrap("mvnd test")
	wrap("gradle build")
	wrap("gradle bootJar")
	wrap("./gradlew test")
	keep("gradle run")
	keep("gradle bootRun")
	keep("gradle :app:bootRun")
	keep("./gradlew :app:run")
	keep("gradlew quarkusDev")
	keep("gradle test --continuous")
	keep("gradle build -t")
	keep("gradle init")
	wrap("dotnet build")
	wrap("dotnet test")
	wrap("dotnet pack")
	wrap("dotnet clean")
	wrap("dotnet format")
	wrap("dotnet run")
	keep("dotnet watch run")
	return cases
}

// TestStreamsForeverCoverage drives streamsForever directly for the spellings
// whose program has no wrapping row (so TestRewrite cannot tell a refusal from
// "never wrapped") and for finite siblings that must stay wrapped.
func TestStreamsForeverCoverage(t *testing.T) {
	tests := []struct {
		program string
		args    string
		want    bool
	}{
		{"cargo", "watch -x test", true},
		{"cargo", "build", false},
		{"cargo", "+nightly watch", true},
		{"next", "dev", true},
		{"next", "start", true},
		{"next", "build", false},
		{"playwright", "show-report", true},
		{"playwright", "show-trace t.zip", true},
		{"playwright", "codegen", true},
		{"playwright", "open", true},
		{"playwright", "test", false},
		{"pnpm", "turbo dev", true},
		{"pnpm", "turbo build", false},
		{"yarn", "playwright test --ui", true},
		{"npx", "nx serve app", true},
		{"nx", "serve app", true},
		{"nx", "graph", true},
		{"nx", "dev", true},
		{"nx", "build app", false},
		{"nx", "run-many --targets=lint,serve", true},
		{"nx", "run-many -t lint,test", false},
		{"turbo", "run dev", true},
		{"turbo", "run build", false},
		{"bun", "test --watch", true},
		{"bun", "test", false},
		{"deno", "test --watch-hmr", true},
		{"deno", "test", false},
		{"prisma", "studio", true},
		{"prisma", "generate", false},
		{"ty", "server", true},
		{"ty", "check", false},
		{"ruff", "server", true},
		{"ruff", "check --watch", true},
		{"ruff", "check", false},
		{"ptw", "", true},
		{"pytest-watch", "", true},
		{"pytest", "tests -f", true},
		{"pytest", "tests", false},
		{"dmypy", "run -- src", true},
		{"dmypy", "start", true},
		{"dmypy", "status", false},
		{"dotnet", "watch run", true},
		{"dotnet", "new console", true},
		{"dotnet", "build", false},
		{"rails", "server", true},
		{"rails", "s", true},
		{"rails", "console", true},
		{"rails", "c", true},
		{"rails", "dbconsole", true},
		{"rails", "db", true},
		{"rails", "runner x.rb", true},
		{"rails", "test", false},
		{"rails", "routes", false},
		{"guard", "", true},
		{"rackup", "", true},
		{"puma", "", true},
		{"foreman", "start", true},
		{"sidekiq", "", true},
		{"solargraph", "stdio", true},
		{"spring", "rspec", true},
		{"bundle", "exec spring rspec", true},
		{"bundle", "exec foreman start", true},
		{"bundle", "exec sidekiq", true},
		{"bundle", "exec rackup", true},
		{"bundle", "exec rails runner x", true},
		{"bundle", "exec rspec", false},
		{"bundle", "install", false},
		{"php", "artisan serve", true},
		{"php", "artisan tinker", true},
		{"php", "artisan queue:work", true},
		{"php", "artisan queue:listen", true},
		{"php", "artisan schedule:work", true},
		{"php", "artisan horizon", true},
		{"php", "artisan reverb:start", true},
		{"php", "artisan migrate", false},
		{"php", "vendor/bin/phpunit", false},
		{"gradle", "bootTestRun", true},
		{"gradle", "jettyRun", true},
		{"gradle", "appRun", true},
		{"gradle", "tomcatRun", true},
		{"gradle", "runIde", true},
		{"gradle", "runClient", true},
		{"gradle", "runServer", true},
		{"gradle", "build --watch-fs", false},
		{"gradle", "build --continuous --watch-fs", true},
		{"./gradlew", ":svc:quarkusDev", true},
		{"mvn", "spring-boot:start", true},
		{"mvn", "quarkus:run", true},
		{"mvn", "jetty:run-war", true},
		{"mvn", "tomcat:run", true},
		{"mvn", "tomcat7:run", true},
		{"mvn", "tomcat9:run", true},
		{"mvn", "exec:exec", true},
		{"mvn", "liberty:dev", true},
		{"mvn", "liberty:run", true},
		{"mvn", "wildfly:run", true},
		{"mvn", "cargo:run", true},
		{"mvn", "cargo:start", true},
		{"mvn", "gwt:run", true},
		{"mvn", "fabric8:watch", true},
		{"mvn", "release:perform", true},
		{"mvn", "release:perform --batch-mode", false},
		{"mvn", "org.springframework.boot:spring-boot-maven-plugin:3.2.0:run", true},
		{"mvn", "io.quarkus:quarkus-maven-plugin:dev", true},
		{"mvn", "spring-boot:build-image", false},
		{"mvn", "test", false},
		{"./mvnw", "spring-boot:run", true},
		{"mvnd", "quarkus:dev", true},
		{"just", "--watch build", true},
		{"just", "-w build", true},
		{"task", "--watch build", true},
		{"task", "-w build", true},
		{"mise", "run -w build", true},
		{"task", "--list", false},
		{"ping", "-c 3 host", false},
		{"ping", "-c3 host", false},
		{"ping", "-w3 host", false},
		{"ping", "--count=3 host", false},
		{"ping", "host", true},
		{"ping", "-t 5 host", true},
		{"lsof", "-r", true},
		{"lsof", "+r 1", true},
		{"lsof", "-r5", true},
		{"lsof", "-i :80", false},
		{"lsof", "-p 1", false},
		{"aws", "logs tail g --follow", true},
		{"aws", "logs tail g", false},
		{"aws", "logs describe-log-groups --follow", false},
		{"aws", "ssm start-session --target i", true},
		{"aws", "ecs execute-command --cluster c", true},
		{"aws", "sso login", true},
		{"aws", "sso login --profile p", true},
		{"aws", "sts get-caller-identity", false},
		{"gcloud", "app logs tail", true},
		{"gcloud", "beta run services logs tail x", true},
		{"gcloud", "compute start-iap-tunnel vm 22", true},
		{"gcloud", "functions logs read", false},
		{"az", "webapp log tail", true},
		{"az", "webapp log download", false},
		{"az", "configure", true},
		{"az", "interactive", true},
		{"pulumi", "watch", true},
		{"pulumi", "logs --follow", true},
		{"pulumi", "up", false},
		{"terraform", "console", true},
		{"terraform", "apply", true},
		{"terraform", "apply -auto-approve", false},
		{"terraform", "apply -auto-approve=true", false},
		{"terraform", "apply -input=false", false},
		{"terraform", "init", false},
		{"tofu", "destroy", true},
		{"tofu", "destroy -auto-approve", false},
		{"ansible-playbook", "--ask-vault-password x", true},
		{"ansible-playbook", "x.yml", false},
		{"pyright", "--watch", true},
		{"basedpyright", "-w", true},
		{"pyright", "src", false},
		{"black", "--watch .", true},
		{"gotestsum", "--watch", true},
		{"gotestsum", "--watch=true", true},
		{"gotestsum", "--format dots", false},
		{"rubocop", "--lsp", true},
		{"phpstan", "analyse --watch", true},
		{"ast-grep", "lsp", true},
		{"sg", "scan --interactive", true},
		{"sg", "scan", false},
		{"docker", "compose up --wait", false},
		{"docker", "compose up", true},
		{"podman", "logs -f c", true},
		{"podman", "events", true},
		{"podman", "attach c", true},
		{"podman", "stats", true},
		{"podman", "stats --no-stream", false},
		{"podman", "ps", false},
	}
	for _, tt := range tests {
		t.Run(tt.program+" "+tt.args, func(t *testing.T) {
			if got := streamsForever(tt.program, strings.Fields(tt.args)); got != tt.want {
				t.Errorf("streamsForever(%q, %q) = %v, want %v", tt.program, tt.args, got, tt.want)
			}
		})
	}
}

// TestTaskRunnerOperands pins the recipe grammar behind just/task/mise: the
// first operand must be a finite verb, later ones a finite verb or a value.
func TestTaskRunnerOperands(t *testing.T) {
	tests := []struct {
		program, args string
		want          bool
	}{
		{"just", "", true},
		{"task", "", true},
		{"mise", "run", true},
		{"just", "build", false},
		{"just", "test ./...", false},
		{"just", "test NAME=x", false},
		{"just", "test 3", false},
		{"just", "test serve", true},
		{"just", "build test", false},
		{"just", "deploy", true},
		{"just", "build -- --flag", false},
		{"task", "test:unit", true},
		{"mise", "run test", false},
		{"mise", "r lint", false},
		{"mise", "run serve", true},
		{"just", "--list", false},
		{"just", "-l", false},
		{"task", "--version", false},
	}
	for _, tt := range tests {
		t.Run(tt.program+" "+tt.args, func(t *testing.T) {
			if got := taskRunnerStreams(tt.program, strings.Fields(tt.args)); got != tt.want {
				t.Errorf("taskRunnerStreams(%q, %q) = %v, want %v", tt.program, tt.args, got, tt.want)
			}
		})
	}
}
