//go:build kyse

package activitylog

import (
	activitylog "github.com/hyz-is/arandu-activitylog"
)

@go
// ShowData is what the handler hands this page.
type ShowData = activitylog.ShowPageData
@endgo

@extends('layouts.app')

@section('content')
	<div class="space-y-6">
		<header class="flex flex-wrap items-start justify-between gap-4">
			<div>
				<h1 class="text-2xl font-semibold tracking-tight">{{ .Row.Description }}</h1>
				<p class="text-muted-foreground mt-1 text-sm">{{ .Row.When }} · {{ .Row.Log }} · {{ .Row.Event }}</p>
			</div>
			<a class="btn" data-variant="outline" data-size="sm" href="{{ .Prefix }}">{{ .Labels.T("back") }}</a>
		</header>

		<dl class="card grid gap-2 p-4 text-sm">
			<div><dt class="font-medium">{{ .Labels.T("subject") }}</dt><dd><code>{{ .Row.Subject }}</code></dd></div>
			<div><dt class="font-medium">{{ .Labels.T("causer") }}</dt><dd>{{ .Row.Causer }}</dd></div>
		</dl>

		<section class="space-y-2" aria-labelledby="activity-changes">
			<h2 id="activity-changes" class="text-lg font-semibold">{{ .Labels.T("changes") }}</h2>
			@if(len(.Changes) == 0)
				<p class="text-muted-foreground text-sm">{{ .Labels.T("no_changes") }}</p>
			@else
				<div class="table-container" role="region" tabindex="0" aria-labelledby="activity-changes">
					<table class="table">
						<thead><tr><th scope="col">{{ .Labels.T("attribute") }}</th><th scope="col">{{ .Labels.T("old") }}</th><th scope="col">{{ .Labels.T("new") }}</th></tr></thead>
						<tbody>
							@foreach(.Changes as change)
								<tr><td><code>{{ change.Attribute }}</code></td><td>{{ change.Old }}</td><td>{{ change.New }}</td></tr>
							@endforeach
						</tbody>
					</table>
				</div>
			@endif
		</section>

		<section class="space-y-2" aria-labelledby="activity-properties">
			<h2 id="activity-properties" class="text-lg font-semibold">{{ .Labels.T("properties") }}</h2>
			@if(len(.Properties) == 0)
				<p class="text-muted-foreground text-sm">{{ .Labels.T("no_properties") }}</p>
			@else
				<div class="table-container" role="region" tabindex="0" aria-labelledby="activity-properties">
					<table class="table">
						<thead><tr><th scope="col">{{ .Labels.T("key") }}</th><th scope="col">{{ .Labels.T("value") }}</th></tr></thead>
						<tbody>
							@foreach(.Properties as property)
								<tr><td><code>{{ property.Key }}</code></td><td>{{ property.Value }}</td></tr>
							@endforeach
						</tbody>
					</table>
				</div>
			@endif
		</section>
	</div>
@endsection
