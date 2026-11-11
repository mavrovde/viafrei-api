#!/usr/bin/env perl
# docs-html.pl BASE_DIR < page.html > page.html
#
# Post-processes one page body rendered by marked for the documentation
# portal (scripts/docs-build.sh):
#
# - every heading gets an id, its text in lower case with every run of other
#   characters folded to "-" (operationIds become their lower-case form, so
#   "#gethealth" reaches "## getHealth"); a repeated id gets "-2", "-3", ...
# - a link to a Markdown file that is a page of the portal points at that
#   page (DOCS_PAGES: space-separated "source=page" pairs), keeping its
#   fragment;
# - a link to any other file of the repository points at it on GitHub, since
#   the portal does not carry it, and only when that file exists in the
#   repository (this script's parent directory); a link to a file that does not exist becomes
#   "missing-in-repo/<path>", which the build refuses;
# - links with a scheme, fragment-only links and links to files the portal
#   itself serves (DOCS_SITE_FILES) are left as they are;
# - code blocks, and the wrapper each table is put in, scroll sideways when
#   wide and can take the keyboard focus, so they can be scrolled without a
#   mouse (WCAG 2.1.1); the table itself keeps its table layout, so its cells
#   stay header-associated for assistive technology and for axe;
# - a task-list item's checkbox is labelled by the item's text (WCAG 1.3.1).
#
# BASE_DIR is the directory the source file sits in, relative to the
# repository root ("." for the root), against which its links resolve.
use strict;
use warnings;
use FindBin;

my $base = shift // die "usage: docs-html.pl BASE_DIR\n";
my %page = map { split /=/, $_, 2 } split ' ', ($ENV{DOCS_PAGES} // '');
my %site = map { $_ => 1 } split ' ', ($ENV{DOCS_SITE_FILES} // '');
my $root = "$FindBin::Bin/..";
my $repo = $ENV{DOCS_REPO_URL} // 'https://github.com/mavrovde/viafrei-api/blob/main';

local $/;
my $html = <STDIN>;

my %seen;
$html =~ s{<h([1-6])>(.*?)</h\1>}{
  my ($level, $text) = ($1, $2);
  (my $id = lc $text) =~ s/<[^>]*>//g;
  $id =~ s/&[a-z]+;|&#\d+;//g;
  $id =~ s/[^a-z0-9]+/-/g;
  $id =~ s/^-+|-+$//g;
  $id = 'section' if $id eq '';
  my $n = ++$seen{$id};
  $id .= "-$n" if $n > 1;
  qq{<h$level id="$id">$text</h$level>};
}gse;

sub resolve {
  my ($path) = @_;
  my @parts = $base eq '.' ? () : split m{/}, $base;
  for my $p (split m{/}, $path) {
    next if $p eq '.' || $p eq '';
    if ($p eq '..') { pop @parts; next }
    push @parts, $p;
  }
  return join '/', @parts;
}

$html =~ s{href="([^"]*)"}{
  my $href = $1;
  my $out = $href;
  if ($href !~ m{^(?:[a-z][a-z0-9+.-]*:|#|/)}i && $href ne '') {
    my ($path, $frag) = $href =~ m{^([^#]*)(#.*)?$};
    $frag //= '';
    if ($site{$path}) {
      $out = $href;
    } else {
      my $file = resolve($path);
      if (exists $page{$file}) {
        $out = $page{$file} . $frag;
      } elsif ($file ne '' && -e "$root/$file") {
        $out = "$repo/$file$frag";
      } else {
        # Not a page and not a file of the repository: a misspelt link. It
        # is left pointing nowhere on the site, so the build refuses it.
        $out = "missing-in-repo/$file$frag";
      }
    }
  }
  qq{href="$out"};
}ge;

$html =~ s{<pre>}{<pre tabindex="0">}g;
$html =~ s{<table>(.*?)</table>}{<div class="table-wrap" tabindex="0"><table>$1</table></div>}gs;
$html =~ s{<li><input([^>]*type="checkbox"[^>]*)>(.*?)</li>}{<li><label><input$1>$2</label></li>}gs;

print $html;
