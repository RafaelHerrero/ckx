# ckx

Switch between AWS (EKS) and Azure (AKS) Kubernetes clusters from one command.

## 🤔 Why?

Juggling multiple AWS accounts and Azure subscriptions means repeating the same steps over and over:
- Remembering the right AWS profile or Azure subscription
- Logging in (AWS SSO / `az login`)
- Finding the cluster and updating kubeconfig
- Trying to remember which clusters belong to which account

`ckx` does all of that in a single command.

## 🎯 What it does

- Detects whether a name is an AWS profile or an Azure subscription
- Logs you in when credentials are missing or expired (`aws sso login` / `az login`)
- Lists the clusters and updates kubeconfig for the one you pick
- Lists and searches AWS profiles and Azure subscriptions together

## Prerequisites

**AWS (EKS)**
- AWS CLI installed
- AWS SSO configured
- AWS profiles set up in `~/.aws/config`

**Azure (AKS)**
- Azure CLI (`az`) installed
- [`kubelogin`](https://github.com/Azure/kubelogin) installed (for Entra ID enabled clusters)

**Both**
- `kubectl` installed

Each cloud is optional: if `az` isn't installed, Azure is simply skipped (and vice versa for AWS without a config file).

## AWS Profile Configuration

Ensure your AWS profiles are properly configured in `~/.aws/config`:

```ini
[profile dev]
sso_start_url = https://your-sso-url.awsapps.com/start
sso_region = eu-central-1
sso_account_id = 123456789012
sso_role_name = YourRole
region = eu-central-1
```

## Usage

```bash
ckx [command]
```

### Interactive picker

Running `ckx` with no subcommand lists every AWS profile and Azure subscription in [fzf](https://github.com/junegunn/fzf). The profile you pick is passed to `ckx use`, so you go on to choose a cluster as usual. Press `Esc` to cancel. You need `fzf` installed (`brew install fzf`).

```bash
ckx            # pick a profile with fzf, then use it
ckx --export   # same, but print the export line for eval
```

### Available Commands

- `completion`: Generate the autocompletion script for the specified shell
- `generate-profiles`: Generate AWS profiles for all SSO accounts and roles
- `help`: Help about any command
- `list`: List available AWS profiles and Azure subscriptions
- `search`: Search profiles and subscriptions by name (case-insensitive substring match)
- `use`: Log in and set kubeconfig for a cluster
- `version`: Print the version

### Use Command

```bash
ckx use <profile-or-subscription> [--provider aws|azure]
ckx use --all
```

The provider is **auto-detected** from the name: AWS profiles come from `~/.aws/config`, Azure subscriptions (name or ID) from `az account list`. If the same name exists in both, `ckx` tells you to pass `--provider`.

**Examples:**
```bash
# AWS profile
ckx use my-aws-profile

# Azure subscription (by name or ID)
ckx use "My Azure Subscription"

# Name exists in both clouds
ckx use shared-name --provider azure

# Pick from the clusters of ALL Azure subscriptions
ckx use --all
```

**AWS:** if credentials are expired, `ckx` runs `aws sso login --profile <profile>` and retries. The kubeconfig entry uses `aws eks get-token`.

**Azure:** if the token is expired or the subscription isn't known to the current session, `ckx` runs `az login`. It then runs `az aks get-credentials` followed by `kubelogin convert-kubeconfig --login azurecli`, so `kubectl` works right away.

**Extra CA bundle (Azure):** if `~/.cert/combined-ca-bundle.pem` exists (for example a corporate TLS-intercepting proxy CA), it is appended to the cluster's CA in your kubeconfig. Without that file this step is skipped.

**Flags:**
- `--provider`: force `aws` or `azure` instead of auto-detecting
- `--all`: list the clusters of every Azure subscription and pick one (no profile argument needed)
- `--export`: print a shell line to `eval` (`export AWS_PROFILE=...` or `export AZURE_SUBSCRIPTION_ID=...`); informational output goes to stderr

#### Shell wrapper (recommended)

A subprocess cannot modify the parent shell's environment, so to also set `AWS_PROFILE` in your current shell use a function in your `.zshrc` or `.bashrc`:

```sh
# ~/.zshrc
cx() {
  if [[ $# -eq 0 || "$1" == "use" ]]; then
    eval "$(ckx "$@" --export)"
  else
    ckx "$@"
  fi
}
```

```bash
cx                         # fzf picker, then exports the env var
cx use my-aws-profile      # switches profile, updates kubeconfig, exports AWS_PROFILE
cx use "My Subscription"   # exports AZURE_SUBSCRIPTION_ID
cx list                    # lists AWS profiles and Azure subscriptions
```

### List and Search

```bash
ckx list
ckx search prod       # matches any profile or subscription containing "prod"
```

Results are labeled with their provider:

```
Profiles matching "prod":
prod-eu-admin (aws)
Prod Subscription (azure)
```

### Generate Profiles Command (AWS only)

The `generate-profiles` command creates AWS profiles for all accounts and roles accessible through your SSO configuration, named `<account-name>-<role-name>`.

**Important:**
- If you do not have a `~/.aws/config` file, you must provide `--sso-start-url`. The tool will create a minimal config file for you.
- If you already have a config file with SSO configuration, the flag is optional and will override the SSO start URL for that run.

**Options:**
- `--dry-run`: Show what profiles would be generated without writing to the config file
- `--region, -r`: Default AWS region for generated profiles (default "eu-central-1")
- `--sso-start-url`: Override or set the SSO start URL (required if no config file exists)

**Prerequisites:**
- You must be logged in to AWS SSO (run `aws sso login --profile DEFAULT-SSO` after the first run)

```bash
ckx generate-profiles --dry-run --sso-start-url https://your-sso-url.awsapps.com/start
ckx generate-profiles --sso-start-url https://your-sso-url.awsapps.com/start
ckx generate-profiles   # if you already have a config file
```

Azure has no equivalent: subscriptions are discovered from `az account list`.

## 🔧 Installation

### Download Latest Release (Recommended)

1. Visit the releases page: https://github.com/eimarfandino/ckx/releases
2. Download the appropriate binary for your operating system

```bash
chmod +x ckx
sudo mv ckx /usr/local/bin/
```

### Manual Installation (Build from Source)

```bash
git clone https://github.com/eimarfandino/ckx.git
cd ckx
make build        # builds ./ckx
make install      # or install into $(go env GOPATH)/bin
```

Run `make help` to see all targets (`build`, `run`, `install`, `test`, `vet`, `fmt`, `clean`). Use `make run ARGS="use my-profile"` to build and run in one step.

### Homebrew (recommended)

```bash
brew install eimarfandino/tap/ckx
```

Upgrade with `brew upgrade ckx`. The formula only installs `ckx`; install the cloud CLIs you use separately:

```bash
brew install awscli kubernetes-cli          # AWS
brew install azure-cli kubelogin            # Azure
```

Verify:
```bash
ckx --help
```

## Global Flags

- `-h, --help`: Display help information for ckx

## 🤝 Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 📝 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 👥 Authors

- Eimar Fandino - Initial work
