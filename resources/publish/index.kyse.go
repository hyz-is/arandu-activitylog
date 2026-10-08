//go:build kyse

package activitylog

import (
	activitylog "github.com/hyz-is/arandu-activitylog"
)

@go
// IndexData is what the handler hands this page: an alias, so the shape is
// written once, beside the code that fills it.
type IndexData = activitylog.IndexPageData
@endgo

@extends('layouts.app')

@section('content')
	<div class="space-y-6">
		<header>
			<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("title") }}</h1>
			<p class="text-muted-foreground mt-1 text-sm">{{ .Labels.T("lead") }}</p>
		</header>

		<form class="flex flex-wrap items-end gap-2" method="get" action="{{ .Prefix }}">
			<label class="grid gap-1 text-sm">{{ .Labels.T("log") }}<input class="input" name="log" value="{{ .Log }}"></label>
			<label class="grid gap-1 text-sm">{{ .Labels.T("event") }}<input class="input" name="event" value="{{ .Event }}"></label>
			<label class="grid gap-1 text-sm grow">{{ .Labels.T("search") }}<input class="input" type="search" name="q" value="{{ .Search }}"></label>
			<button class="btn" data-variant="outline" type="submit">{{ .Labels.T("filter") }}</button>
		</form>

		@if(len(.Rows) == 0)
			<p class="text-muted-foreground text-sm">{{ .Labels.T("empty") }}</p>
		@else
			<div class="table-container" role="region" tabindex="0" aria-label="{{ .Labels.T("title") }}">
				<table class="table">
					<thead>
						<tr>
							<th scope="col">{{ .Labels.T("when") }}</th>
							<th scope="col">{{ .Labels.T("log") }}</th>
							<th scope="col">{{ .Labels.T("description") }}</th>
							<th scope="col">{{ .Labels.T("event") }}</th>
							<th scope="col">{{ .Labels.T("subject") }}</th>
							<th scope="col">{{ .Labels.T("causer") }}</th>
						</tr>
					</thead>
					<tbody>
						@foreach(.Rows as row)
							<tr>
								<td>{{ row.When }}</td>
								<td><span class="badge" data-variant="outline">{{ row.Log }}</span></td>
								<td><a class="font-medium" href="{{ row.URL }}">{{ row.Description }}</a></td>
								<td>{{ row.Event }}</td>
								<td><code>{{ row.Subject }}</code></td>
								<td>{{ row.Causer }}</td>
							</tr>
						@endforeach
					</tbody>
				</table>
			</div>
			<nav class="flex items-center justify-between text-sm" aria-label="{{ .Labels.T("page") }}">
				<span>{{ .Labels.T("page") }} {{ .PageNumber }} {{ .Labels.T("of") }} {{ .Pages }} · {{ .Total }}</span>
				<span class="flex gap-2">
					@if(.PreviousURL != "")
						<a class="btn" data-variant="outline" data-size="sm" href="{{ .PreviousURL }}">{{ .Labels.T("previous") }}</a>
					@endif
					@if(.NextURL != "")
						<a class="btn" data-variant="outline" data-size="sm" href="{{ .NextURL }}">{{ .Labels.T("next") }}</a>
					@endif
				</span>
			</nav>
		@endif
	</div>
@endsection
