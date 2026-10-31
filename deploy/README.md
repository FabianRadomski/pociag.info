# Deploying

Every push to `main` runs `.github/workflows/deploy.yml`: vet, test, build a
static binary, then pipe it over SSH to the server. The deploy key can only run
`sudo -n /usr/local/sbin/pociag-deploy`, which:

- reads the binary from stdin (refuses empty, oversized or non-ELF input),
- keeps the current binary as `/usr/local/bin/pociag.prev`,
- installs the new one and restarts the `pociag` OpenRC service,
- checks it answers on `http://127.0.0.1:8080/`, and rolls back to
  `pociag.prev` if it doesn't (the workflow then fails).

The worst a stolen deploy key can do is install a bad pociag binary, which runs
as the unprivileged `pociag` user. That only holds while the script is
root-owned and not writable by the deploy user.

You can also run the workflow by hand from the Actions tab (`workflow_dispatch`),
e.g. to redeploy.

## One-time setup

Below, `deploy` stands for the account the key logs in as and `HOST` for the
server's address. Neither belongs in this repo.

1. Generate the key pair locally:

   ```sh
   ssh-keygen -t ed25519 -N '' -C pociag-deploy -f deploy_key
   ```

2. Install the script and the sudo rule on the server, as root:

   ```sh
   install -o root -g root -m 0755 pociag-deploy /usr/local/sbin/pociag-deploy
   echo 'deploy ALL=(root) NOPASSWD: /usr/local/sbin/pociag-deploy ""' > /etc/sudoers.d/pociag-deploy
   chmod 0440 /etc/sudoers.d/pociag-deploy
   visudo -c
   ```

   The trailing `""` means the rule only matches when no arguments are passed.

3. Append the public key to the deploy account's `~/.ssh/authorized_keys`,
   prefixed with the restrictions (one line):

   ```
   restrict,command="sudo -n /usr/local/sbin/pociag-deploy" ssh-ed25519 AAAA... pociag-deploy
   ```

   `restrict` turns off PTY, port, agent and X11 forwarding; `command=` runs
   the deploy whatever command the client asked for.

4. Get the host key from the server itself rather than trusting a scan, and
   build the known_hosts line with the address the workflow connects to:

   ```sh
   echo "HOST $(ssh <your-usual-alias> cat /etc/ssh/ssh_host_ed25519_key.pub | cut -d' ' -f1,2)"
   ```

5. Check from your machine that the key works and is locked down:

   ```sh
   ssh -i deploy_key deploy@HOST < /dev/null   # should fail with "no binary on stdin"
   ```

6. Add the repo secrets:

   ```sh
   gh secret set FROG_DEPLOY_KEY < deploy_key
   gh secret set FROG_KNOWN_HOSTS --body 'HOST ssh-ed25519 AAAA...'
   gh secret set FROG_SSH_TARGET --body 'deploy@HOST'
   ```

   Then delete the local `deploy_key`.

To approve each deploy by hand, add `environment: production` to the job and
give that environment required reviewers under Settings → Environments.
