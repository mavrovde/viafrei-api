#!/usr/bin/env perl
# docs-check.pl SITE_DIR
#
# The offline checks of the built documentation portal (#20), run by
# scripts/docs-build.sh after every page is written, so a failure fails the
# docs build:
#
# - links: every href and src that is not an absolute URL must name a file of
#   the site, and its fragment an id of that page (a broken internal link);
# - ids: no id may appear twice on a page (a fragment would be ambiguous);
# - diagrams: every diagram must have an equivalent text description. A
#   Mermaid block must be followed by a paragraph that starts with
#   "Text description:"; an image needs a non-empty alt text; an inline SVG
#   on a guide page needs a <title>.
#
# Every page is read; reference.html, which Redoc renders, is checked for
# links and ids only, since its icons are not diagrams. Prints every page it
# read and fails closed when it read none. External links are not fetched.
# Exit 0 clean, 1 findings, 2 refused (no site, no page, an unreadable page).
use strict;
use warnings;

my $site = shift // die "usage: docs-check.pl SITE_DIR\n";
sub refuse { print STDERR "FAIL: $_[0]\n"; exit 2 }
opendir(my $dh, $site) or refuse("cannot read $site: $!");
my @pages = sort grep { /\.html\z/ && -f "$site/$_" } readdir $dh;
closedir $dh;
refuse("no page found in $site: refusing to report a clean site") unless @pages;

my (%html, %ids);
for my $p (@pages) {
  open(my $fh, '<:raw', "$site/$p") or refuse("cannot read $site/$p: $!");
  local $/;
  $html{$p} = <$fh>;
  close $fh;
  $ids{$p}{$1}++ while $html{$p} =~ /\s(?:id|name)="([^"]+)"/g;
}

my @findings;
my ($links, $diagrams) = (0, 0);
for my $p (@pages) {
  my $h = $html{$p};
  for my $id (sort keys %{ $ids{$p} }) {
    push @findings, "$p: id \"$id\" appears $ids{$p}{$id} times" if $ids{$p}{$id} > 1;
  }
  while ($h =~ /\s(?:href|src)="([^"]*)"/g) {
    my $ref = $1;
    next if $ref =~ m{^(?:[a-z][a-z0-9+.-]*:|//)}i;    # absolute: not fetched
    $links++;
    my ($file, $frag) = $ref =~ m{^([^#?]*)(?:\?[^#]*)?(?:#(.*))?\z};
    $file = $p if $file eq '';
    if ($file =~ m{(?:^|/)\.\.(?:/|\z)} || $file =~ m{^/}) {
      push @findings, "$p: link \"$ref\" leaves the site";
      next;
    }
    if (!-e "$site/$file") {
      push @findings, "$p: link \"$ref\" names no file of the site";
      next;
    }
    next unless defined $frag && $frag ne '';
    if (!exists $html{$file}) {
      push @findings, "$p: link \"$ref\" has a fragment into a file that is not a page";
    } elsif (!$ids{$file}{$frag}) {
      push @findings, "$p: link \"$ref\" names no id of $file";
    }
  }
  next if $p eq 'reference.html';
  while ($h =~ /<pre[^>]*><code class="language-mermaid">.*?<\/code><\/pre>\s*(<p>(?:<(?:strong|em)>)?\s*Text description:)?/gs) {
    $diagrams++;
    push @findings, "$p: diagram " . $diagrams . " has no text description (a paragraph starting \"Text description:\" right after it)" unless $1;
  }
  while ($h =~ /(<img\b[^>]*>)/g) {
    my $img = $1;
    $diagrams++;
    push @findings, "$p: image without alt text: $img" unless $img =~ /\salt="[^"]*\S[^"]*"/;
  }
  while ($h =~ /(<svg\b.*?<\/svg>)/gs) {
    $diagrams++;
    push @findings, "$p: inline SVG without a <title>" unless $1 =~ /<title>\s*\S/;
  }
}

print "docs check: read $_\n" for @pages;
if (@findings) {
  print STDERR "FAIL: $_\n" for @findings;
  printf STDERR "FAIL: %d finding(s) over %d page(s)\n", scalar @findings, scalar @pages;
  exit 1;
}
printf "docs check: %d page(s), %d internal link(s) resolved, %d diagram(s) described\n", scalar @pages, $links, $diagrams;
